# Space Members Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make adding and removing space members work, through one shared set of internals that the Share modal, Space Settings → Permissions, and the space info panel each wrap.

**Architecture:** Four layers, bottom-up. A singular data-source method per server route; two reducer cases that splice a space's member-id array; singular thunks plus a bulk add thunk composed from the singular one via `Promise.allSettled`; one hook that owns toasts and busy state; and a presentational core in `components/space_members/` that receives functions rather than calling the hook itself. Read-only is expressed as an absent `actions` prop, so a row can never render a menu with nothing behind it.

**Tech Stack:** TypeScript, React 17-style function components, Redux (hand-rolled reducers, no RTK), `react-intl` for all user-visible copy, Base UI (`@base-ui-components/react`) for the combobox and menu primitives, Jest + `@testing-library/react`.

## Global Constraints

- **Spec:** `docs/superpowers/specs/2026-08-06-share-modal-members-design.md`. Read it before Task 1.
- **No comments that narrate what the code does.** A comment is allowed only for a non-obvious *why* (a workaround, a subtle invariant, a counter-intuitive trade-off) or as a doc comment on an exported symbol. Match the density of the surrounding files.
- **Every user-visible string goes through `react-intl`** — `<FormattedMessage>` in JSX, `formatMessage` in callbacks. Never a bare string literal in the UI.
- **New message ids are namespaced `docs.spaceMembers.*`.** The three existing per-surface duplicates (`docs.share.handle`, `docs.spaceSettings.permissions.handle`, `docs.spaceInfo.handle`, and the role labels alongside them) are deleted as their surfaces are ported.
- **Roles are scaffolding.** `Admin` / `Can edit` / `Can view` render `disabled` and do nothing. Do not wire them. Per-member capabilities land in PR #10 (MM-69269).
- **Do not touch** the Permissions tab's public/private selector or external-sharing toggle, `store/permissions.ts`, or anything server-side.
- **Membership changes never mark the settings modal dirty.** They are committed the moment they return, so they must stay out of `useInfoTab`'s `SaveChangesBar` flow.
- **Run from `webapp/`:** `npx jest <path>` for one file, `npm test` for all, `npm run check-types`, `npm run lint`.
- **Commit style:** `<type>(<scope>): <subject>`, imperative, ≤50 chars, no period, no AI attribution. Body for anything non-obvious.

---

### Task 1: Reducer cases for one member added or removed

**Files:**
- Modify: `webapp/src/store/action_types.ts` (add to `SpaceTypes`)
- Modify: `webapp/src/store/entities.ts` (the `spaceMembers` reducer, currently ~line 286)
- Test: `webapp/src/store/entities.test.ts` (new `describe('spaceMembers')` block)

**Interfaces:**
- Consumes: nothing.
- Produces: `SpaceTypes.ADDED_SPACE_MEMBER` and `SpaceTypes.REMOVED_SPACE_MEMBER`, both dispatched as `{type, spaceId: string, userId: string}`.

Two invariants carry the design and both are tested here: a no-op returns the *identical* state object (so consumers don't re-render), and an add never seeds an absent space entry (because `areMembersLoadedForSpace` reads presence to mean "loaded").

- [ ] **Step 1: Write the failing tests**

Append to `webapp/src/store/entities.test.ts`:

```ts
describe('spaceMembers', () => {
    const initialState = reducer(undefined, {type: '@@INIT'});
    const loaded = reducer(initialState, {
        type: SpaceTypes.RECEIVED_SPACE_MEMBERS,
        spaceId: 's1',
        userIds: ['u1', 'u2'],
    });

    it('ADDED_SPACE_MEMBER appends to a loaded roster', () => {
        const next = reducer(loaded, {type: SpaceTypes.ADDED_SPACE_MEMBER, spaceId: 's1', userId: 'u3'});

        expect(next.spaceMembers.s1).toEqual(['u1', 'u2', 'u3']);
    });

    // A no-op must not produce a new object, or every consumer re-renders for nothing.
    it('ADDED_SPACE_MEMBER is identity when the member is already there', () => {
        const next = reducer(loaded, {type: SpaceTypes.ADDED_SPACE_MEMBER, spaceId: 's1', userId: 'u1'});

        expect(next.spaceMembers).toBe(loaded.spaceMembers);
    });

    // Presence of the entry is what areMembersLoadedForSpace reads as "loaded", so a
    // single added id must not claim the whole roster has arrived.
    it('ADDED_SPACE_MEMBER does not seed a space whose members were never loaded', () => {
        const next = reducer(initialState, {type: SpaceTypes.ADDED_SPACE_MEMBER, spaceId: 'other', userId: 'u9'});

        expect('other' in next.spaceMembers).toBe(false);
        expect(next.spaceMembers).toBe(initialState.spaceMembers);
    });

    it('REMOVED_SPACE_MEMBER drops the member', () => {
        const next = reducer(loaded, {type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId: 's1', userId: 'u1'});

        expect(next.spaceMembers.s1).toEqual(['u2']);
    });

    it('REMOVED_SPACE_MEMBER is identity for an absent member or an unloaded space', () => {
        const absentMember = reducer(loaded, {type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId: 's1', userId: 'u9'});
        expect(absentMember.spaceMembers).toBe(loaded.spaceMembers);

        const unloadedSpace = reducer(loaded, {type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId: 'other', userId: 'u1'});
        expect(unloadedSpace.spaceMembers).toBe(loaded.spaceMembers);
    });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd webapp && npx jest src/store/entities.test.ts -t spaceMembers`
Expected: FAIL — the new action types are `undefined`, so the reducer hits `default` and returns state unchanged; the append and drop assertions fail.

- [ ] **Step 3: Add the action types**

In `webapp/src/store/action_types.ts`, inside `SpaceTypes`, after `RECEIVED_SPACE_MEMBERS`:

```ts
    ADDED_SPACE_MEMBER: manifest.id + '_added_space_member',
    REMOVED_SPACE_MEMBER: manifest.id + '_removed_space_member',
```

- [ ] **Step 4: Add the reducer cases**

In `webapp/src/store/entities.ts`, add the action shape next to the other action types near the top of the file (beside `ReceivedSpaceMembersAction`):

```ts
type SpaceMemberAction = {spaceId: string; userId: string};
```

Then add these two cases to the `spaceMembers` reducer, before its `case SpaceTypes.DELETED_SPACE`:

```ts
    case SpaceTypes.ADDED_SPACE_MEMBER: {
        const {spaceId, userId} = action as unknown as SpaceMemberAction;
        const current = state[spaceId];

        // An absent entry means the roster was never loaded. Seeding it from a single
        // id would make areMembersLoadedForSpace claim a full list; fetchSpaceMembers
        // is what populates it.
        if (!current || current.includes(userId)) {
            return state;
        }
        return {...state, [spaceId]: [...current, userId]};
    }
    case SpaceTypes.REMOVED_SPACE_MEMBER: {
        const {spaceId, userId} = action as unknown as SpaceMemberAction;
        const current = state[spaceId];
        if (!current?.includes(userId)) {
            return state;
        }
        return {...state, [spaceId]: current.filter((id) => id !== userId)};
    }
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd webapp && npx jest src/store/entities.test.ts`
Expected: PASS — all cases, old and new.

- [ ] **Step 6: Type-check and commit**

```bash
cd webapp && npm run check-types
git add webapp/src/store/action_types.ts webapp/src/store/entities.ts webapp/src/store/entities.test.ts
git commit -m "feat(docs): splice one space member in the store"
```

---

### Task 2: Data source method and the three thunks

**Files:**
- Modify: `webapp/src/data/docs_data_source.ts` (interface, beside `removeSpaceMember` ~line 40)
- Modify: `webapp/src/data/api_data_source.ts` (implementation, beside `removeSpaceMember` ~line 53)
- Modify: `webapp/src/store/actions.ts` (thunks + one error predicate)
- Test: `webapp/src/store/actions.test.ts`

**Interfaces:**
- Consumes: `SpaceTypes.ADDED_SPACE_MEMBER`, `SpaceTypes.REMOVED_SPACE_MEMBER` from Task 1.
- Produces:
  - `docsDataSource.addSpaceMember(spaceId: string, userId: string): Promise<SpaceMember>`
  - `addSpaceMember(spaceId: string, userId: string): DocsThunkAction<Promise<void>>` — rejects on failure
  - `removeSpaceMember(spaceId: string, userId: string): DocsThunkAction<Promise<void>>` — rejects on failure
  - `addSpaceMembers(spaceId: string, userIds: string[]): DocsThunkAction<Promise<FailedMemberAdd[]>>` — never rejects
  - `export type FailedMemberAdd = {userId: string; error: unknown}`
  - `isNotTeamMemberError(error: unknown): boolean`

The shape is the point: `addSpaceMembers` is a *caller* of `addSpaceMember`, not a generalisation of it. The singular thunk keeps rejecting so a single add reads naturally anywhere; the wrapper absorbs `allSettled`.

- [ ] **Step 1: Write the failing tests**

In `webapp/src/store/actions.test.ts`, add a mock fn beside the existing ones and extend the `jest.mock('data')` factory:

```ts
const mockAddSpaceMember = jest.fn();
```

```ts
jest.mock('data', () => ({
    docsDataSource: {
        addSpaceMember: (...args: unknown[]) => mockAddSpaceMember(...args as []),
        removeSpaceMember: (...args: unknown[]) => mockRemoveSpaceMember(...args as []),
        movePage: (...args: unknown[]) => mockMovePage(...args as []),
        listPages: (...args: unknown[]) => mockListPages(...args as []),
    },
}));
```

Extend the import at the top of the file:

```ts
import {addSpaceMember, addSpaceMembers, isLastSpaceMemberError, leaveSpace, movePage, removeSpaceMember} from './actions';
import {SpaceTypes} from './action_types';
```

Then append:

```ts
const added = (userId: string) => ({type: SpaceTypes.ADDED_SPACE_MEMBER, spaceId: 'space1', userId});

describe('addSpaceMember', () => {
    beforeEach(() => jest.clearAllMocks());

    it('dispatches the add once the server accepts it', async () => {
        mockAddSpaceMember.mockResolvedValue({user_id: 'u1'});

        const {result, dispatch} = run((d, g) => addSpaceMember('space1', 'u1')(d as never, g as never, undefined as never));
        await result;

        expect(mockAddSpaceMember).toHaveBeenCalledWith('space1', 'u1');
        expect(dispatch).toHaveBeenCalledWith(added('u1'));
    });

    // It rejects rather than swallowing, because only the caller can tell a 403
    // ("not on this team") apart from a fault worth a generic message.
    it('rejects and dispatches nothing when the server refuses', async () => {
        mockAddSpaceMember.mockRejectedValue(new Error('nope'));

        const {result, dispatch} = run((d, g) => addSpaceMember('space1', 'u1')(d as never, g as never, undefined as never));

        await expect(result).rejects.toThrow('nope');
        expect(dispatch).not.toHaveBeenCalledWith(added('u1'));
    });
});

describe('removeSpaceMember', () => {
    beforeEach(() => jest.clearAllMocks());

    it('dispatches the removal once the server accepts it', async () => {
        mockRemoveSpaceMember.mockResolvedValue(undefined);

        const {result, dispatch} = run((d, g) => removeSpaceMember('space1', 'u1')(d as never, g as never, undefined as never));
        await result;

        expect(mockRemoveSpaceMember).toHaveBeenCalledWith('space1', 'u1');
        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId: 'space1', userId: 'u1'});
    });

    it('rejects when the server refuses, leaving the store alone', async () => {
        mockRemoveSpaceMember.mockRejectedValue(new Error('nope'));

        const {result, dispatch} = run((d, g) => removeSpaceMember('space1', 'u1')(d as never, g as never, undefined as never));

        await expect(result).rejects.toThrow('nope');
        expect(dispatch).not.toHaveBeenCalledWith({type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId: 'space1', userId: 'u1'});
    });
});

describe('addSpaceMembers', () => {
    beforeEach(() => jest.clearAllMocks());

    // A partly-failed batch has no single outcome, so the successes have already
    // landed by the time the wrapper resolves with the failures.
    it('resolves with only the failures and still dispatches the successes', async () => {
        const refusal = new Error('not on this team');
        mockAddSpaceMember.
            mockResolvedValueOnce({user_id: 'u1'}).
            mockRejectedValueOnce(refusal).
            mockResolvedValueOnce({user_id: 'u3'});

        const {result, dispatch} = run((d, g) => addSpaceMembers('space1', ['u1', 'u2', 'u3'])(d as never, g as never, undefined as never));
        const failed = await result;

        expect(failed).toEqual([{userId: 'u2', error: refusal}]);
        expect(dispatch).toHaveBeenCalledWith(added('u1'));
        expect(dispatch).toHaveBeenCalledWith(added('u3'));
        expect(dispatch).not.toHaveBeenCalledWith(added('u2'));
    });

    it('never rejects, even when every add fails', async () => {
        mockAddSpaceMember.mockRejectedValue(new Error('nope'));

        const {result} = run((d, g) => addSpaceMembers('space1', ['u1', 'u2'])(d as never, g as never, undefined as never));

        await expect(result).resolves.toHaveLength(2);
    });

    it('resolves empty for an empty batch without calling the server', async () => {
        const {result} = run((d, g) => addSpaceMembers('space1', [])(d as never, g as never, undefined as never));

        await expect(result).resolves.toEqual([]);
        expect(mockAddSpaceMember).not.toHaveBeenCalled();
    });
});

describe('isNotTeamMemberError', () => {
    it('recognises the server 403 and nothing else', () => {
        expect(isNotTeamMemberError(new ClientError('', {message: 'nope', status_code: 403, url: '/x'}))).toBe(true);
        expect(isNotTeamMemberError(new ClientError('', {message: 'nope', status_code: 409, url: '/x'}))).toBe(false);
        expect(isNotTeamMemberError(new Error('boom'))).toBe(false);
    });
});
```

Add `isNotTeamMemberError` to the import list from `./actions`. `ClientError` is already imported at the top of this test file.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd webapp && npx jest src/store/actions.test.ts`
Expected: FAIL — `addSpaceMember`, `addSpaceMembers` and `isNotTeamMemberError` are not exported.

- [ ] **Step 3: Add the data source method**

In `webapp/src/data/docs_data_source.ts`, immediately above `removeSpaceMember`:

```ts
    // Adds a user to a space. The server requires the target to be an active member
    // of the space's team (403 otherwise) and rejects an unknown user (404). There is
    // no bulk route: adding several people is several calls.
    addSpaceMember(spaceId: string, userId: string): Promise<SpaceMember>;
```

In `webapp/src/data/api_data_source.ts`, immediately above `removeSpaceMember`:

```ts
    addSpaceMember: (spaceId, userId) =>
        restPost<SpaceMember>(`${apiUrl()}/spaces/${seg(spaceId)}/members`, {user_id: userId}),
```

- [ ] **Step 4: Add the thunks**

In `webapp/src/store/actions.ts`, immediately after `fetchSpaceMembers`:

```ts
/**
 * Adds one member to a space.
 *
 * Rejects on failure: only the caller can tell the server's 403 ("not a member of
 * this team") apart from a fault that deserves a generic message.
 *
 * Not `leaveSpace`, which removes the *current* user and drops the whole space from
 * the store. This only edits the member array.
 */
export function addSpaceMember(spaceId: string, userId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        await docsDataSource.addSpaceMember(spaceId, userId);
        dispatch({type: SpaceTypes.ADDED_SPACE_MEMBER, spaceId, userId});
    };
}

/**
 * Removes one member from a space. Rejects on failure so the caller can recognise
 * the last-member 409 (see isLastSpaceMemberError).
 *
 * Not `leaveSpace`: that removes the current user and prunes the space. This leaves
 * the space in place and only edits its member array.
 */
export function removeSpaceMember(spaceId: string, userId: string): DocsThunkAction<Promise<void>> {
    return async (dispatch) => {
        await docsDataSource.removeSpaceMember(spaceId, userId);
        dispatch({type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId, userId});
    };
}

export type FailedMemberAdd = {userId: string; error: unknown};

/**
 * Adds several members by dispatching addSpaceMember once per user, concurrently.
 *
 * Never rejects. A batch has no single outcome to reject with, so the result is the
 * users that failed and why — an empty array means every add landed. Each success has
 * already dispatched by the time this resolves, so the store is right even for a
 * batch that partly failed.
 *
 * The raw `error` is passed back rather than a message: choosing wording belongs with
 * the other message selection, in useManageSpaceMembers.
 */
export function addSpaceMembers(spaceId: string, userIds: string[]): DocsThunkAction<Promise<FailedMemberAdd[]>> {
    return async (dispatch) => {
        const settled = await Promise.allSettled(
            userIds.map((userId) => dispatch(addSpaceMember(spaceId, userId))),
        );
        return settled.flatMap((result, i) => (
            result.status === 'rejected' ? [{userId: userIds[i], error: result.reason}] : []
        ));
    };
}
```

Then, directly below the existing `isLastSpaceMemberError`:

```ts
// The add route answers 403 when the target isn't an active member of the space's
// team. That is the one add failure a user can act on, so it gets its own message;
// like isLastSpaceMemberError, the status is all the REST layer preserves.
export function isNotTeamMemberError(error: unknown): boolean {
    return error instanceof ClientError && error.status_code === 403;
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd webapp && npx jest src/store/actions.test.ts`
Expected: PASS.

- [ ] **Step 6: Type-check and commit**

```bash
cd webapp && npm run check-types
git add webapp/src/data/docs_data_source.ts webapp/src/data/api_data_source.ts webapp/src/store/actions.ts webapp/src/store/actions.test.ts
git commit -m "feat(docs): add space member thunks, bulk over singular"
```

---

### Task 3: The `useManageSpaceMembers` hook

**Files:**
- Create: `webapp/src/hooks/space_members.ts`
- Test: `webapp/src/hooks/space_members.test.tsx`

**Interfaces:**
- Consumes: `addSpaceMembers`, `removeSpaceMember`, `isLastSpaceMemberError`, `isNotTeamMemberError`, `FailedMemberAdd` from Task 2; `useLeaveSpace` from `hooks/leave_space`; `MemberProfile` from `hooks/members`.
- Produces:

```ts
export type ManageSpaceMembers = {
    addMembers: (users: MemberProfile[]) => Promise<MemberProfile[]>;  // resolves to the FAILED users
    removeMember: (userId: string) => Promise<void>;
    leave: () => Promise<void>;
    busy: boolean;
};
export function useManageSpaceMembers(space: Space): ManageSpaceMembers;
```

This is the only layer that knows about both profiles and copy. Thunks deal in ids and errors; components deal in chips and rows.

**Copy note:** removing *someone else* gets its own last-member string. The existing `docs.leaveSpace.error.lastMember` ends "…before you leave", which is wrong when you are removing another person.

- [ ] **Step 1: Write the failing tests**

Create `webapp/src/hooks/space_members.test.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';

import {ClientError} from '@mattermost/client';

import {makeSpace} from 'store/test_fixtures';

import {toast} from 'components/toast';

import type {MemberProfile} from './members';
import {useManageSpaceMembers} from './space_members';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockAddSpaceMembers = jest.fn();
const mockRemoveSpaceMember = jest.fn();
const mockLeaveSpace = jest.fn();

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    addSpaceMembers: (...args: unknown[]) => () => mockAddSpaceMembers(...args as []),
    removeSpaceMember: (...args: unknown[]) => () => mockRemoveSpaceMember(...args as []),
}));

jest.mock('./leave_space', () => ({useLeaveSpace: () => mockLeaveSpace}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

const space = makeSpace('space-1', 'Engineering');

const profile = (id: string, displayName: string): MemberProfile => ({
    id,
    displayName,
    username: displayName.toLowerCase(),
    avatarUrl: '',
});

const render = () => {
    const store = makeTestStore();
    const wrapper = ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>
            <IntlProvider
                locale='en'
                messages={{}}
            >
                {children}
            </IntlProvider>
        </Provider>
    );

    return renderHook(() => useManageSpaceMembers(space), {wrapper}).result;
};

const clientError = (status: number) => new ClientError('', {message: 'nope', status_code: status, url: '/x'});

describe('useManageSpaceMembers', () => {
    beforeEach(() => jest.clearAllMocks());

    it('adds every user and reports no failures', async () => {
        mockAddSpaceMembers.mockResolvedValue([]);
        const users = [profile('u1', 'Ada'), profile('u2', 'Grace')];
        const hook = render();

        let failed: MemberProfile[] = [];
        await act(async () => {
            failed = await hook.current.addMembers(users);
        });

        expect(mockAddSpaceMembers).toHaveBeenCalledWith('space-1', ['u1', 'u2']);
        expect(failed).toEqual([]);
        expect(toast.error).not.toHaveBeenCalled();
    });

    // The failed ids come back from the thunk; the caller needs the profiles it
    // passed in so it can restore exactly those chips.
    it('maps failed ids back to the profiles it was given', async () => {
        mockAddSpaceMembers.mockResolvedValue([{userId: 'u2', error: clientError(403)}]);
        const users = [profile('u1', 'Ada'), profile('u2', 'Grace')];
        const hook = render();

        let failed: MemberProfile[] = [];
        await act(async () => {
            failed = await hook.current.addMembers(users);
        });

        expect(failed).toEqual([users[1]]);
    });

    // A 403 is the one add failure the user can act on, so it says so by name.
    it('names the user and the reason for a single 403', async () => {
        mockAddSpaceMembers.mockResolvedValue([{userId: 'u2', error: clientError(403)}]);
        const hook = render();

        await act(async () => {
            await hook.current.addMembers([profile('u2', 'Grace')]);
        });

        expect(toast.error).toHaveBeenCalledWith("Grace isn't a member of this team.");
    });

    it('collapses a multi-failure batch to a count', async () => {
        mockAddSpaceMembers.mockResolvedValue([
            {userId: 'u1', error: clientError(403)},
            {userId: 'u2', error: clientError(500)},
        ]);
        const hook = render();

        await act(async () => {
            await hook.current.addMembers([profile('u1', 'Ada'), profile('u2', 'Grace')]);
        });

        expect(toast.error).toHaveBeenCalledWith("Couldn't add 2 people. Please try again.");
    });

    it('distinguishes the last-member refusal when removing someone', async () => {
        mockRemoveSpaceMember.mockRejectedValue(clientError(409));
        const hook = render();

        await act(async () => {
            await hook.current.removeMember('u1');
        });

        expect(toast.error).toHaveBeenCalledWith('A space must keep at least one member with access.');
    });

    it('reports any other removal failure generically', async () => {
        mockRemoveSpaceMember.mockRejectedValue(clientError(500));
        const hook = render();

        await act(async () => {
            await hook.current.removeMember('u1');
        });

        expect(toast.error).toHaveBeenCalledWith('Something went wrong. Please try again.');
    });

    // Leaving is one behaviour across the header menu, the sidebar row and here.
    it('delegates leaving to useLeaveSpace', async () => {
        mockLeaveSpace.mockResolvedValue(undefined);
        const hook = render();

        await act(async () => {
            await hook.current.leave();
        });

        expect(mockLeaveSpace).toHaveBeenCalled();
        expect(toast.error).not.toHaveBeenCalled();
    });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd webapp && npx jest src/hooks/space_members.test.tsx`
Expected: FAIL — `Cannot find module './space_members'`.

- [ ] **Step 3: Write the hook**

Create `webapp/src/hooks/space_members.ts`:

```ts
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useCallback, useState} from 'react';
import {useIntl} from 'react-intl';

import {addSpaceMembers, isLastSpaceMemberError, isNotTeamMemberError, removeSpaceMember} from 'store/actions';

import {toast} from 'components/toast';

import type {Space} from 'types/docs';

import {useLeaveSpace} from './leave_space';
import {useAppDispatch} from './redux';

export type ManageSpaceMembers = {

    /** Resolves to the users that FAILED, so a caller can restore exactly those chips. */
    addMembers: (users: MemberProfile[]) => Promise<MemberProfile[]>;
    removeMember: (userId: string) => Promise<void>;
    leave: () => Promise<void>;

    /** A mutation is in flight; write affordances should be disabled. */
    busy: boolean;
};

/**
 * Membership mutations for a space, with their user-facing messages.
 *
 * The only layer that knows about both profiles and copy: the thunks below it deal
 * in ids and errors, the components above it deal in chips and rows. Each writing
 * surface calls this once and threads the functions into the shared
 * `components/space_members` core, which never calls it itself.
 */
export function useManageSpaceMembers(space: Space): ManageSpaceMembers {
    const dispatch = useAppDispatch();
    const {formatMessage} = useIntl();
    const leaveSpace = useLeaveSpace(space);
    const [busy, setBusy] = useState(false);

    const addMembers = useCallback(async (users: MemberProfile[]): Promise<MemberProfile[]> => {
        setBusy(true);
        try {
            const failed = await dispatch(addSpaceMembers(space.id, users.map((user) => user.id)));
            if (failed.length === 0) {
                return [];
            }

            const byId = new Map(users.map((user) => [user.id, user]));
            const failedUsers = failed.flatMap(({userId}) => {
                const user = byId.get(userId);
                return user ? [user] : [];
            });

            if (failed.length === 1) {
                const name = failedUsers[0]?.displayName ?? '';
                toast.error(isNotTeamMemberError(failed[0].error) ? formatMessage({
                    id: 'docs.spaceMembers.add.error.notTeamMember',
                    defaultMessage: "{name} isn't a member of this team.",
                }, {name}) : formatMessage({
                    id: 'docs.spaceMembers.add.error.single',
                    defaultMessage: "Couldn't add {name}. Please try again.",
                }, {name}));
            } else {
                // One toast per user would stack N of them for a single click. The chips
                // that stay in the picker are what identify which ones failed.
                toast.error(formatMessage({
                    id: 'docs.spaceMembers.add.error.several',
                    defaultMessage: "Couldn't add {count} people. Please try again.",
                }, {count: failed.length}));
            }
            return failedUsers;
        } finally {
            setBusy(false);
        }
    }, [dispatch, space.id, formatMessage]);

    const removeMember = useCallback(async (userId: string) => {
        setBusy(true);
        try {
            await dispatch(removeSpaceMember(space.id, userId));
        } catch (error) {
            // Its own string rather than useLeaveSpace's: that one ends "before you
            // leave", which is wrong when you are removing somebody else.
            toast.error(isLastSpaceMemberError(error) ? formatMessage({
                id: 'docs.spaceMembers.remove.error.lastMember',
                defaultMessage: 'A space must keep at least one member with access.',
            }) : formatMessage({
                id: 'docs.spaceMembers.remove.error.generic',
                defaultMessage: 'Something went wrong. Please try again.',
            }));
        } finally {
            setBusy(false);
        }
    }, [dispatch, space.id, formatMessage]);

    const leave = useCallback(async () => {
        setBusy(true);
        try {
            await leaveSpace();
        } finally {
            setBusy(false);
        }
    }, [leaveSpace]);

    return {addMembers, removeMember, leave, busy};
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd webapp && npx jest src/hooks/space_members.test.tsx`
Expected: PASS — all eight cases.

- [ ] **Step 5: Type-check, lint and commit**

```bash
cd webapp && npm run check-types && npx eslint src/hooks/space_members.ts src/hooks/space_members.test.tsx
git add webapp/src/hooks/space_members.ts webapp/src/hooks/space_members.test.tsx
git commit -m "feat(docs): add a space membership hook with its messages"
```

---

### Task 4: The shared roster — read-only

**Files:**
- Create: `webapp/src/components/space_members/member_row.tsx`
- Create: `webapp/src/components/space_members/member_list.tsx`
- Create: `webapp/src/components/space_members/space_members.module.scss`
- Create: `webapp/src/components/space_members/index.ts`
- Test: `webapp/src/components/space_members/member_list.test.tsx`

**Interfaces:**
- Consumes: `MemberProfile` from `hooks/members`.
- Produces:

```ts
// member_row.tsx
type MemberRowProps = {
    member: MemberProfile;
    avatarSize: 'sm' | 'md';
    isCurrentUser: boolean;
    showYouBadge: boolean;
    trailing?: React.ReactNode;
};

// member_list.tsx  (default export MemberList)
type MemberListProps = {
    members: MemberProfile[];
    avatarSize: 'sm' | 'md';
    showYouBadge?: boolean;
};

// index.ts
export {default as MemberList} from './member_list';
```

Read-only first. Task 5 adds the `actions` prop and the menu; the space info panel (Task 8) never needs more than this.

- [ ] **Step 1: Write the failing test**

Create `webapp/src/components/space_members/member_list.test.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import type {MemberProfile} from 'hooks/members';

import MemberList from './member_list';

import {renderWithContext} from '../../../tests/react_testing_utils';

const profile = (id: string, displayName: string): MemberProfile => ({
    id,
    displayName,
    username: displayName.toLowerCase(),
    avatarUrl: '',
});

const members = [profile('u1', 'Ada'), profile('u2', 'Grace')];

describe('MemberList', () => {
    it('renders a row per member with name and handle', () => {
        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
            />,
        );

        expect(screen.getByText('Ada')).toBeInTheDocument();
        expect(screen.getByText('@ada')).toBeInTheDocument();
        expect(screen.getByText('Grace')).toBeInTheDocument();
        expect(screen.getByText('@grace')).toBeInTheDocument();
    });

    it('marks the current user only when asked to', () => {
        const state = {currentUser: {id: 'u1', username: 'ada'}};

        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
                showYouBadge={true}
            />,
            {state},
        );
        expect(screen.getByText('(You)')).toBeInTheDocument();

        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
            />,
            {state},
        );
        expect(screen.getAllByText('(You)')).toHaveLength(1);
    });

    it('renders nothing for an empty roster', () => {
        renderWithContext(
            <MemberList
                members={[]}
                avatarSize='sm'
            />,
        );

        expect(screen.queryByText('@ada')).not.toBeInTheDocument();
    });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd webapp && npx jest src/components/space_members/member_list.test.tsx`
Expected: FAIL — `Cannot find module './member_list'`.

- [ ] **Step 3: Write the stylesheet**

Create `webapp/src/components/space_members/space_members.module.scss`, moving the rules verbatim from `share_space_modal.module.scss` (lines 17–57) so the Share modal's appearance does not change:

```scss
.memberList {
    display: flex;
    flex-direction: column;
}

.memberRow {
    display: flex;
    min-height: 48px;
    align-items: center;
    gap: 12px;
    padding: 8px 0;
}

.memberInfo {
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: baseline;
    gap: 6px;
}

.memberName {
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 600;
    white-space: nowrap;
}

.memberUsername,
.you {
    overflow: hidden;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 14px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.roleTrigger {
    gap: 4px;
    color: var(--link-color);
    font-weight: 600;
}
```

- [ ] **Step 4: Write the row**

Create `webapp/src/components/space_members/member_row.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React from 'react';
import {FormattedMessage} from 'react-intl';
import {Avatar} from 'webapp_globals';

import styles from './space_members.module.scss';

type Props = {
    member: MemberProfile;
    avatarSize: 'sm' | 'md';
    isCurrentUser: boolean;
    showYouBadge: boolean;

    /** Row-end slot. Absent on a read-only roster. */
    trailing?: React.ReactNode;
};

const MemberRow = ({member, avatarSize, isCurrentUser, showYouBadge, trailing}: Props) => (
    <div className={styles.memberRow}>
        <Avatar
            url={member.avatarUrl}
            username={member.username}
            size={avatarSize}
            name=''
        />
        <span className={styles.memberInfo}>
            <span className={styles.memberName}>{member.displayName}</span>
            {member.username && (
                <span className={styles.memberUsername}>
                    <FormattedMessage
                        id='docs.spaceMembers.handle'
                        defaultMessage='@{username}'
                        values={{username: member.username}}
                    />
                </span>
            )}
            {showYouBadge && isCurrentUser && (
                <span className={styles.you}>
                    <FormattedMessage
                        id='docs.spaceMembers.you'
                        defaultMessage='(You)'
                    />
                </span>
            )}
        </span>
        {trailing}
    </div>
);

export default MemberRow;
```

- [ ] **Step 5: Write the list and the barrel**

Create `webapp/src/components/space_members/member_list.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useAppSelector} from 'hooks/redux';
import React from 'react';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import MemberRow from './member_row';
import styles from './space_members.module.scss';

type Props = {
    members: MemberProfile[];
    avatarSize: 'sm' | 'md';
    showYouBadge?: boolean;
};

/**
 * A space's member roster. Shared by the Share modal, Space Settings → Permissions
 * and the space info panel, which differ only in chrome and affordances.
 */
const MemberList = ({members, avatarSize, showYouBadge = false}: Props) => {
    const currentUserId = useAppSelector(getCurrentUserId);

    return (
        <div className={styles.memberList}>
            {members.map((member) => (
                <MemberRow
                    key={member.id}
                    member={member}
                    avatarSize={avatarSize}
                    isCurrentUser={member.id === currentUserId}
                    showYouBadge={showYouBadge}
                />
            ))}
        </div>
    );
};

export default MemberList;
```

Create `webapp/src/components/space_members/index.ts`:

```ts
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export {default as MemberList} from './member_list';
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `cd webapp && npx jest src/components/space_members/member_list.test.tsx`
Expected: PASS.

- [ ] **Step 7: Type-check, lint and commit**

```bash
cd webapp && npm run check-types && npx eslint src/components/space_members
git add webapp/src/components/space_members
git commit -m "feat(docs): add a shared space member roster"
```

---

### Task 5: The row menu and the `actions` prop

**Files:**
- Create: `webapp/src/components/space_members/member_row_menu.tsx`
- Modify: `webapp/src/components/space_members/member_list.tsx` (accept `actions`)
- Modify: `webapp/src/components/space_members/index.ts` (export the actions type)
- Test: `webapp/src/components/space_members/member_row_menu.test.tsx`
- Test: `webapp/src/components/space_members/member_list.test.tsx` (add the actions cases)

**Interfaces:**
- Consumes: `MemberRow`, `MemberList` from Task 4; `Menu` from `components/menu`; `Button` from `components/form_controls/button`.
- Produces:

```ts
// member_list.tsx
export type MemberListActions = {
    onRemove: (userId: string) => void;
    onLeave: () => void;
    disabled: boolean;
};
// MemberListProps gains:  actions?: MemberListActions;

// member_row_menu.tsx (default export MemberRowMenu)
type MemberRowMenuProps = {
    member: MemberProfile;
    isCurrentUser: boolean;
    disabled: boolean;
    onRemove: () => void;
    onLeave: () => void;
};
```

`actions` is an optional bag rather than a `readOnly` flag: a read-only roster is one that was given no actions, so a row can never render a menu with nothing behind it, and nothing branches on which surface it is in.

- [ ] **Step 1: Write the failing tests**

Create `webapp/src/components/space_members/member_row_menu.test.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';

import type {MemberProfile} from 'hooks/members';

import MemberRowMenu from './member_row_menu';

import {renderWithContext} from '../../../tests/react_testing_utils';

const member: MemberProfile = {id: 'u1', displayName: 'Ada', username: 'ada', avatarUrl: ''};

const renderMenu = (props: Partial<React.ComponentProps<typeof MemberRowMenu>> = {}) => renderWithContext(
    <MemberRowMenu
        member={member}
        isCurrentUser={false}
        disabled={false}
        onRemove={jest.fn()}
        onLeave={jest.fn()}
        {...props}
    />,
);

describe('MemberRowMenu', () => {
    it('offers Remove for another member, and the role items are disabled', async () => {
        const onRemove = jest.fn();
        renderMenu({onRemove});

        await userEvent.click(screen.getByRole('button', {name: /Ada/}));

        // Roles are PR #10 scaffolding: shown so the menu keeps its shape, but inert.
        expect(screen.getByRole('menuitem', {name: 'Admin'})).toHaveAttribute('aria-disabled', 'true');
        expect(screen.getByRole('menuitem', {name: 'Can edit'})).toHaveAttribute('aria-disabled', 'true');

        await userEvent.click(screen.getByRole('menuitem', {name: 'Remove from space'}));
        expect(onRemove).toHaveBeenCalled();
    });

    it('offers Leave space on your own row instead of Remove', async () => {
        const onLeave = jest.fn();
        renderMenu({isCurrentUser: true, onLeave});

        await userEvent.click(screen.getByRole('button', {name: /Ada/}));

        expect(screen.queryByRole('menuitem', {name: 'Remove from space'})).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole('menuitem', {name: 'Leave space'}));
        expect(onLeave).toHaveBeenCalled();
    });

    // The trigger still opens while busy, so the roles stay readable and the
    // unavailable action is visibly the thing that is unavailable.
    it('disables only the action while a mutation is in flight', async () => {
        const onRemove = jest.fn();
        renderMenu({disabled: true, onRemove});

        await userEvent.click(screen.getByRole('button', {name: /Ada/}));

        expect(screen.getByRole('menuitem', {name: 'Remove from space'})).toHaveAttribute('aria-disabled', 'true');
    });
});
```

Append to `webapp/src/components/space_members/member_list.test.tsx`:

```tsx
    it('gives every row a menu when actions are supplied', () => {
        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
                actions={{onRemove: jest.fn(), onLeave: jest.fn(), disabled: false}}
            />,
        );

        expect(screen.getAllByRole('button', {name: /Ada|Grace/})).toHaveLength(2);
    });

    // Read-only is the absence of actions, not a flag — so there is no way to render
    // a menu with nothing behind it.
    it('renders no menu at all without actions', () => {
        renderWithContext(
            <MemberList
                members={members}
                avatarSize='sm'
            />,
        );

        expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd webapp && npx jest src/components/space_members`
Expected: FAIL — `Cannot find module './member_row_menu'`, and `MemberList` rejects the `actions` prop.

- [ ] **Step 3: Write the menu**

Create `webapp/src/components/space_members/member_row_menu.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';

import {Button} from 'components/form_controls/button';
import Menu from 'components/menu/menu';

import styles from './space_members.module.scss';

type Props = {
    member: MemberProfile;
    isCurrentUser: boolean;

    /** A mutation is in flight; the action is unavailable but the menu still opens. */
    disabled: boolean;
    onRemove: () => void;
    onLeave: () => void;
};

/**
 * The role/membership menu on a member row.
 *
 * Role items are rendered disabled rather than hidden, so the menu does not change
 * shape when PR #10's capabilities make them real.
 */
const MemberRowMenu = ({member, isCurrentUser, disabled, onRemove, onLeave}: Props) => {
    const {formatMessage} = useIntl();

    const trigger = (
        <Button
            type='button'
            emphasis='quaternary'
            size='sm'
            className={styles.roleTrigger}
            aria-label={formatMessage(
                {id: 'docs.spaceMembers.menu.label', defaultMessage: 'Manage {name}'},
                {name: member.displayName},
            )}
        >
            <FormattedMessage
                id='docs.spaceMembers.role.admin'
                defaultMessage='Admin'
            />
            <ChevronDownIcon size={16}/>
        </Button>
    );

    return (
        <Menu
            ariaLabel={formatMessage(
                {id: 'docs.spaceMembers.menu.ariaLabel', defaultMessage: 'Membership options for {name}'},
                {name: member.displayName},
            )}
            align='right'
            trigger={trigger}
        >
            <Menu.Item disabled={true}>
                <FormattedMessage
                    id='docs.spaceMembers.role.admin'
                    defaultMessage='Admin'
                />
            </Menu.Item>
            <Menu.Item disabled={true}>
                <FormattedMessage
                    id='docs.spaceMembers.role.canEdit'
                    defaultMessage='Can edit'
                />
            </Menu.Item>
            <Menu.Item disabled={true}>
                <FormattedMessage
                    id='docs.spaceMembers.role.canView'
                    defaultMessage='Can view'
                />
            </Menu.Item>

            <Menu.Separator/>

            {isCurrentUser ? (
                <Menu.Item
                    destructive={true}
                    disabled={disabled}
                    onClick={onLeave}
                >
                    <FormattedMessage
                        id='docs.spaceMembers.leave'
                        defaultMessage='Leave space'
                    />
                </Menu.Item>
            ) : (
                <Menu.Item
                    destructive={true}
                    disabled={disabled}
                    onClick={onRemove}
                >
                    <FormattedMessage
                        id='docs.spaceMembers.remove'
                        defaultMessage='Remove from space'
                    />
                </Menu.Item>
            )}
        </Menu>
    );
};

export default MemberRowMenu;
```

- [ ] **Step 4: Wire `actions` into the list**

In `webapp/src/components/space_members/member_list.tsx`, add the import and type, and pass `trailing`:

```tsx
import MemberRowMenu from './member_row_menu';
```

```tsx
export type MemberListActions = {
    onRemove: (userId: string) => void;
    onLeave: () => void;

    /** A mutation is in flight; row actions are unavailable. */
    disabled: boolean;
};

type Props = {
    members: MemberProfile[];
    avatarSize: 'sm' | 'md';
    showYouBadge?: boolean;

    // Absent means a read-only roster. Expressed as the absence of actions rather
    // than a flag, so a row can never render a menu with nothing behind it.
    actions?: MemberListActions;
};
```

Inside the `map`, replace the `<MemberRow .../>` with:

```tsx
                <MemberRow
                    key={member.id}
                    member={member}
                    avatarSize={avatarSize}
                    isCurrentUser={member.id === currentUserId}
                    showYouBadge={showYouBadge}
                    trailing={actions && (
                        <MemberRowMenu
                            member={member}
                            isCurrentUser={member.id === currentUserId}
                            disabled={actions.disabled}
                            onRemove={() => actions.onRemove(member.id)}
                            onLeave={actions.onLeave}
                        />
                    )}
                />
```

And add to `webapp/src/components/space_members/index.ts`:

```ts
export type {MemberListActions} from './member_list';
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd webapp && npx jest src/components/space_members`
Expected: PASS. If the role items report `disabled` rather than `aria-disabled`, adjust the assertions to match what `components/menu` actually renders — check `components/menu/menu.test.tsx` for the established idiom rather than guessing.

- [ ] **Step 6: Type-check, lint and commit**

```bash
cd webapp && npm run check-types && npx eslint src/components/space_members
git add webapp/src/components/space_members
git commit -m "feat(docs): add a member row menu with remove and leave"
```

---

### Task 6: The add-members field

**Files:**
- Create: `webapp/src/components/space_members/add_members_field.tsx`
- Move: `webapp/src/components/share_space_modal/people_picker.tsx` → `webapp/src/components/space_members/people_picker.tsx`
- Move: `webapp/src/components/share_space_modal/people_picker.module.scss` → `webapp/src/components/space_members/people_picker.module.scss`
- Modify: `webapp/src/components/space_members/index.ts`
- Modify: `webapp/src/components/space_members/space_members.module.scss` (add `.addField`)
- Test: `webapp/src/components/space_members/add_members_field.test.tsx`

**Interfaces:**
- Consumes: `PeoplePicker` (moved, unchanged); `PrimaryButton` from `components/form_controls/button`.
- Produces:

```ts
// add_members_field.tsx (default export AddMembersField)
type AddMembersFieldProps = {
    excludeIds: string[];
    onAdd: (users: MemberProfile[]) => Promise<MemberProfile[]>;  // resolves to failures
    disabled: boolean;
};
// index.ts:  export {default as AddMembersField} from './add_members_field';
```

The field owns the whole add interaction — picker, chips, pending state, Add button, and putting failures back. A consumer passes only the member ids; the field unions its own pending selections into `excludeIds` itself.

- [ ] **Step 1: Write the failing test**

Create `webapp/src/components/space_members/add_members_field.test.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen, waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';

import type {MemberProfile} from 'hooks/members';

import AddMembersField from './add_members_field';

import {renderWithContext} from '../../../tests/react_testing_utils';

const ada = {id: 'u1', displayName: 'Ada', username: 'ada', avatarUrl: ''};
const grace = {id: 'u2', displayName: 'Grace', username: 'grace', avatarUrl: ''};

// The picker's server search is out of scope here; stub it so the test drives
// selection directly and asserts this component's own behaviour.
const mockOnChange = {current: (_users: MemberProfile[]) => {}};

jest.mock('./people_picker', () => ({
    __esModule: true,
    default: ({selected, onChange}: {selected: MemberProfile[]; onChange: (u: MemberProfile[]) => void}) => {
        mockOnChange.current = onChange;
        return <div data-testid='picker'>{selected.map((user) => user.displayName).join(',')}</div>;
    },
}));

const renderField = (onAdd: jest.Mock, disabled = false) => renderWithContext(
    <AddMembersField
        excludeIds={[]}
        onAdd={onAdd}
        disabled={disabled}
    />,
);

const pick = async (users: MemberProfile[]) => {
    await waitFor(() => expect(screen.getByTestId('picker')).toBeInTheDocument());
    mockOnChange.current(users);
};

describe('AddMembersField', () => {
    it('disables Add until something is picked', async () => {
        renderField(jest.fn());

        expect(screen.getByRole('button', {name: 'Add'})).toBeDisabled();

        await pick([ada]);
        await waitFor(() => expect(screen.getByRole('button', {name: 'Add'})).toBeEnabled());
    });

    it('stays disabled while a mutation is in flight', async () => {
        renderField(jest.fn(), true);
        await pick([ada]);

        await waitFor(() => expect(screen.getByTestId('picker')).toHaveTextContent('Ada'));
        expect(screen.getByRole('button', {name: 'Add'})).toBeDisabled();
    });

    it('hands the picked users to onAdd and clears them all on success', async () => {
        const onAdd = jest.fn().mockResolvedValue([]);
        renderField(onAdd);
        await pick([ada, grace]);

        await userEvent.click(await screen.findByRole('button', {name: 'Add'}));

        expect(onAdd).toHaveBeenCalledWith([ada, grace]);
        await waitFor(() => expect(screen.getByTestId('picker')).toHaveTextContent(''));
    });

    // Failed chips stay so the user can see which ones did not land, next to the
    // toast that says why.
    it('keeps only the failed users as chips', async () => {
        const onAdd = jest.fn().mockResolvedValue([grace]);
        renderField(onAdd);
        await pick([ada, grace]);

        await userEvent.click(await screen.findByRole('button', {name: 'Add'}));

        await waitFor(() => expect(screen.getByTestId('picker')).toHaveTextContent('Grace'));
        expect(screen.getByTestId('picker')).not.toHaveTextContent('Ada');
    });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd webapp && npx jest src/components/space_members/add_members_field.test.tsx`
Expected: FAIL — `Cannot find module './add_members_field'`.

- [ ] **Step 3: Move the picker**

```bash
cd /Users/calebroseland/Sources/github-mattermost/mattermost-plugin-docs
git mv webapp/src/components/share_space_modal/people_picker.tsx webapp/src/components/space_members/people_picker.tsx
git mv webapp/src/components/share_space_modal/people_picker.module.scss webapp/src/components/space_members/people_picker.module.scss
```

Its contents are unchanged — the relative `./people_picker.module.scss` import still resolves. Do not edit it.

- [ ] **Step 4: Add the layout rule**

Append to `webapp/src/components/space_members/space_members.module.scss`:

```scss
.addField {
    display: flex;
    align-items: flex-start;
    gap: 8px;
}

.addField > :first-child {
    flex: 1 1 auto;
    min-width: 0;
}
```

- [ ] **Step 5: Write the field**

Create `webapp/src/components/space_members/add_members_field.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React, {useCallback, useMemo, useState} from 'react';
import {FormattedMessage} from 'react-intl';

import {PrimaryButton} from 'components/form_controls/button';

import PeoplePicker from './people_picker';
import styles from './space_members.module.scss';

type Props = {

    /** Members already in the space. Pending selections are excluded on top of these. */
    excludeIds: string[];

    /** Resolves to the users that failed, which stay as chips. */
    onAdd: (users: MemberProfile[]) => Promise<MemberProfile[]>;
    disabled: boolean;
};

/**
 * The add-people control: pick several, then commit them with Add.
 *
 * Owns the pending selection so consumers pass only the current member ids and never
 * have to know that pending chips exist.
 */
const AddMembersField = ({excludeIds, onAdd, disabled}: Props) => {
    const [pending, setPending] = useState<MemberProfile[]>([]);

    const exclude = useMemo(
        () => [...excludeIds, ...pending.map((user) => user.id)],
        [excludeIds, pending],
    );

    const add = useCallback(async () => {
        setPending(await onAdd(pending));
    }, [onAdd, pending]);

    return (
        <div className={styles.addField}>
            <PeoplePicker
                selected={pending}
                excludeIds={exclude}
                onChange={setPending}
            />
            <PrimaryButton
                type='button'
                size='sm'
                disabled={pending.length === 0 || disabled}
                onClick={add}
            >
                <FormattedMessage
                    id='docs.spaceMembers.add'
                    defaultMessage='Add'
                />
            </PrimaryButton>
        </div>
    );
};

export default AddMembersField;
```

- [ ] **Step 6: Export it**

Add to `webapp/src/components/space_members/index.ts`:

```ts
export {default as AddMembersField} from './add_members_field';
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd webapp && npx jest src/components/space_members`
Expected: PASS. The Share modal is now broken (its `./people_picker` import is gone) — that is expected and Task 7 fixes it; `npm run check-types` will fail until then, so skip it this step.

- [ ] **Step 8: Commit**

```bash
git add webapp/src/components/space_members webapp/src/components/share_space_modal
git commit -m "feat(docs): add a shared add-members field"
```

---

### Task 7: Port the Share modal onto the core

**Files:**
- Modify: `webapp/src/components/share_space_modal/share_space_modal.tsx`
- Modify: `webapp/src/components/share_space_modal/share_space_modal.module.scss` (delete the moved rules)
- Test: `webapp/src/components/share_space_modal/share_space_modal.test.tsx` (new file)

**Interfaces:**
- Consumes: `MemberList`, `AddMembersField`, `MemberListActions` from `components/space_members`; `useManageSpaceMembers` from `hooks/space_members`.
- Produces: nothing consumed by later tasks.

The wrapper's whole job is chrome plus threading the hook's functions in. Row and chip behaviour is the core's, already covered by its own tests.

- [ ] **Step 1: Write the failing test**

Create `webapp/src/components/share_space_modal/share_space_modal.test.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen, waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import ShareSpaceModal from './share_space_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const mockAddMembers = jest.fn();
const mockRemoveMember = jest.fn();
const mockLeave = jest.fn();

jest.mock('hooks/space_members', () => ({
    useManageSpaceMembers: () => ({
        addMembers: mockAddMembers,
        removeMember: mockRemoveMember,
        leave: mockLeave,
        busy: false,
    }),
}));

jest.mock('hooks/members', () => ({
    useSpaceMemberProfiles: () => [
        {id: 'me', displayName: 'Caleb', username: 'caleb', avatarUrl: ''},
        {id: 'u2', displayName: 'Ada', username: 'ada', avatarUrl: ''},
    ],
}));

jest.mock('hooks/navigation', () => ({
    useDocsNavigation: () => ({paths: {space: (id: string) => `/team/spaces/${id}`}}),
}));

const space = makeSpace('space-1', 'Engineering');
const state = {currentUser: {id: 'me', username: 'caleb'}};

describe('ShareSpaceModal', () => {
    beforeEach(() => jest.clearAllMocks());

    it('lists the members with the current user marked', () => {
        renderWithContext(
            <ShareSpaceModal
                space={space}
                onClose={jest.fn()}
            />,
            {state},
        );

        expect(screen.getByText('Caleb')).toBeInTheDocument();
        expect(screen.getByText('(You)')).toBeInTheDocument();
        expect(screen.getByText('Ada')).toBeInTheDocument();
    });

    it('removes another member through the hook', async () => {
        renderWithContext(
            <ShareSpaceModal
                space={space}
                onClose={jest.fn()}
            />,
            {state},
        );

        await userEvent.click(screen.getByRole('button', {name: /Ada/}));
        await userEvent.click(screen.getByRole('menuitem', {name: 'Remove from space'}));

        expect(mockRemoveMember).toHaveBeenCalledWith('u2');
    });

    // Leaving destroys your access to what is behind the modal, so the modal goes too.
    it('leaves and closes the modal from your own row', async () => {
        mockLeave.mockResolvedValue(undefined);
        const onClose = jest.fn();

        renderWithContext(
            <ShareSpaceModal
                space={space}
                onClose={onClose}
            />,
            {state},
        );

        await userEvent.click(screen.getByRole('button', {name: /Caleb/}));
        await userEvent.click(screen.getByRole('menuitem', {name: 'Leave space'}));

        expect(mockLeave).toHaveBeenCalled();
        await waitFor(() => expect(onClose).toHaveBeenCalled());
    });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd webapp && npx jest src/components/share_space_modal`
Expected: FAIL — the modal still renders its own inline rows with an inert role button, so no `Remove from space` menu item exists.

- [ ] **Step 3: Rewrite the modal body**

In `webapp/src/components/share_space_modal/share_space_modal.tsx`:

Replace the imports of `MemberProfile`, `PeoplePicker`, `getCurrentUserId`, `useAppSelector` and `ChevronDownIcon`/`Avatar` as they become unused, and add:

```tsx
import {useManageSpaceMembers} from 'hooks/space_members';

import {AddMembersField, MemberList} from 'components/space_members';
```

Replace the leading comment block with:

```tsx
// Members, add and remove are real. The visibility and role dropdowns are
// scaffolding for PR #10's view_access and capabilities.
```

Replace the component's state and body. The header, `titleActions`, and `footer` are unchanged:

```tsx
const ShareSpaceModal = ({space, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const {paths} = useDocsNavigation();
    const members = useSpaceMemberProfiles(space.id);
    const {canManageMembers} = useSpacePermissions(space.id);
    const {addMembers, removeMember, leave, busy} = useManageSpaceMembers(space);

    const memberIds = useMemo(() => members.map((member) => member.id), [members]);

    const actions: MemberListActions | undefined = canManageMembers ? {
        onRemove: removeMember,
        onLeave: () => leave().then(onClose),
        disabled: busy,
    } : undefined;

    // ... title / titleActions / footer unchanged ...
```

and the body:

```tsx
            <div className={styles.body}>
                {canManageMembers && (
                    <AddMembersField
                        excludeIds={memberIds}
                        onAdd={addMembers}
                        disabled={busy}
                    />
                )}
                <MemberList
                    members={members}
                    avatarSize='md'
                    showYouBadge={true}
                    actions={actions}
                />
            </div>
```

Import the actions type alongside the components:

```tsx
import {AddMembersField, MemberList} from 'components/space_members';
import type {MemberListActions} from 'components/space_members';
```

- [ ] **Step 4: Delete the moved styles**

From `webapp/src/components/share_space_modal/share_space_modal.module.scss`, delete `.memberList`, `.memberRow`, `.memberInfo`, `.memberName`, `.memberUsername`, `.you` and `.roleTrigger` (lines 17–57). They now live in `space_members.module.scss`. Keep `.modal`, `.body`, `.copyLink`, `.access`, `.accessLeft`, `.accessTrigger`, `.accessHint` and `.canView`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd webapp && npx jest src/components/share_space_modal src/components/space_members`
Expected: PASS.

- [ ] **Step 6: Full check and commit**

```bash
cd webapp && npm run check-types && npx eslint src && npm test
git add webapp/src/components/share_space_modal webapp/src/components/space_members
git commit -m "feat(docs): wire the Share modal to member add and remove"
```

---

### Task 8: Extract and port Space Settings → Permissions

**Files:**
- Create: `webapp/src/components/space_settings_modal/permissions_tab.tsx`
- Modify: `webapp/src/components/space_settings_modal/space_settings_modal.tsx` (remove `PermissionsTab`, import it; drop now-unused imports)
- Modify: `webapp/src/components/space_settings_modal/space_settings_modal.module.scss` (delete the duplicated member-row rules)
- Test: `webapp/src/components/space_settings_modal/permissions_tab.test.tsx`

**Interfaces:**
- Consumes: `MemberList`, `AddMembersField`, `MemberListActions`; `useManageSpaceMembers`; the `Section` helper and `PublicPrivateSelector` already in `space_settings_modal.tsx`.
- Produces: `export default PermissionsTab` taking `{space: Space}`.

This tab currently hand-rolls its own rows and a search field that is a `div` with `aria-disabled` — not a combobox at all. Both are deleted. `Section` must be exported from `space_settings_modal.tsx` so the extracted tab can use it.

**Do not** connect any of this to `info.dirty` / `SaveChangesBar`: an add is committed when it returns, so offering to discard it would be a lie.

- [ ] **Step 1: Write the failing test**

Create `webapp/src/components/space_settings_modal/permissions_tab.test.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import PermissionsTab from './permissions_tab';

import {renderWithContext} from '../../../tests/react_testing_utils';

const mockAddMembers = jest.fn();
const mockRemoveMember = jest.fn();

jest.mock('hooks/space_members', () => ({
    useManageSpaceMembers: () => ({
        addMembers: mockAddMembers,
        removeMember: mockRemoveMember,
        leave: jest.fn(),
        busy: false,
    }),
}));

jest.mock('hooks/members', () => ({
    useSpaceMemberProfiles: () => [{id: 'u2', displayName: 'Ada', username: 'ada', avatarUrl: ''}],
}));

const space = makeSpace('space-1', 'Engineering');

describe('PermissionsTab', () => {
    beforeEach(() => jest.clearAllMocks());

    // The tab used to fake this with an aria-disabled div; it is a real control now.
    it('offers a working add control', () => {
        renderWithContext(<PermissionsTab space={space}/>);

        expect(screen.getByRole('button', {name: 'Add'})).toBeInTheDocument();
    });

    it('removes a member through the hook', async () => {
        renderWithContext(<PermissionsTab space={space}/>);

        await userEvent.click(screen.getByRole('button', {name: /Ada/}));
        await userEvent.click(screen.getByRole('menuitem', {name: 'Remove from space'}));

        expect(mockRemoveMember).toHaveBeenCalledWith('u2');
    });

    it('keeps the space-access scaffolding in place', () => {
        renderWithContext(<PermissionsTab space={space}/>);

        expect(screen.getByText('Public')).toBeInTheDocument();
        expect(screen.getByText('External sharing')).toBeInTheDocument();
    });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd webapp && npx jest src/components/space_settings_modal/permissions_tab.test.tsx`
Expected: FAIL — `Cannot find module './permissions_tab'`.

- [ ] **Step 3: Export `Section` from the modal**

In `webapp/src/components/space_settings_modal/space_settings_modal.tsx`, change:

```tsx
const Section = ({title, children}: {title: React.ReactNode; children: React.ReactNode}) => (
```

to:

```tsx
export const Section = ({title, children}: {title: React.ReactNode; children: React.ReactNode}) => (
```

- [ ] **Step 4: Create the extracted tab**

Create `webapp/src/components/space_settings_modal/permissions_tab.tsx`. Move the existing `PermissionsTab` function across verbatim *except* the people section, and replace that section's contents with the core:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceMemberProfiles} from 'hooks/members';
import {useManageSpaceMembers} from 'hooks/space_members';
import React, {useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import PublicPrivateSelector from 'components/form_controls/public_private_selector';
import {AddMembersField, MemberList} from 'components/space_members';
import type {MemberListActions} from 'components/space_members';

import type {Space} from 'types/docs';

import {Section} from './space_settings_modal';
import styles from './space_settings_modal.module.scss';

/**
 * Space Settings → Permissions.
 *
 * The people section is the shared member core; the access selector and the
 * external-sharing toggle around it stay scaffolding for PR #10. Membership changes
 * apply immediately and deliberately never mark the modal dirty — an add is already
 * committed when it returns, so SaveChangesBar would imply a discard that cannot
 * happen.
 */
const PermissionsTab = ({space}: {space: Space}) => {
    const {formatMessage} = useIntl();
    const members = useSpaceMemberProfiles(space.id);
    const {addMembers, removeMember, leave, busy} = useManageSpaceMembers(space);

    const memberIds = useMemo(() => members.map((member) => member.id), [members]);

    const actions: MemberListActions = {
        onRemove: removeMember,
        onLeave: leave,
        disabled: busy,
    };

    // Copy lines 383-398 of space_settings_modal.tsx verbatim: the two-option
    // useMemo ending `], [formatMessage]);`. It is untouched scaffolding.
    const accessOptions = useMemo(() => [/* … */], [formatMessage]);

    return (
        <>
            <Section
                title={(
                    <FormattedMessage
                        id='docs.spaceSettings.permissions.accessHeading'
                        defaultMessage='Space access'
                    />
                )}
            >
                <PublicPrivateSelector
                    ariaLabel={formatMessage({id: 'docs.spaceSettings.permissions.accessLabel', defaultMessage: 'Space access'})}
                    options={accessOptions}
                    value='public'
                    onChange={() => {}}
                />
            </Section>

            <Section
                title={(
                    <FormattedMessage
                        id='docs.spaceSettings.permissions.peopleHeading'
                        defaultMessage='People and groups with access'
                    />
                )}
            >
                <AddMembersField
                    excludeIds={memberIds}
                    onAdd={addMembers}
                    disabled={busy}
                />
                <MemberList
                    members={members}
                    avatarSize='sm'
                    actions={actions}
                />
            </Section>

            {/* Copy lines 477-500 of space_settings_modal.tsx verbatim: the
                `<section className={styles.section}>` holding the External sharing
                toggle row and its "Coming soon" pill. Untouched scaffolding. */}
        </>
    );
};

export default PermissionsTab;
```

The two `/* … */` markers above are verbatim copies from `space_settings_modal.tsx`, not code to write: `accessOptions` is lines 383-398 and the external-sharing block is lines 477-500. Neither changes. `PublicPrivateSelector` already lives at `components/form_controls/public_private_selector`, so it needs no re-export; `Section` is the only thing that does.

- [ ] **Step 5: Remove the old tab and its dead imports**

Delete the `PermissionsTab` function from `space_settings_modal.tsx` and add:

```tsx
import PermissionsTab from './permissions_tab';
```

Then remove any import that is now unused — `useSpaceMemberProfiles`, `Avatar`, `ChevronDownIcon`, `MagnifyIcon`, `GlobeIcon`, `LockOutlineIcon` are the likely candidates. `npm run lint` will name them.

- [ ] **Step 6: Delete the duplicated styles**

From `space_settings_modal.module.scss`, delete the member-row rules the tab no longer uses — `.memberList`, `.memberRow`, `.memberInfo`, `.memberName`, `.memberUsername`, `.roleTrigger`, and `.search` / `.mutedIcon` if nothing else references them. Grep before deleting: `rtk proxy grep -rn "styles.search\|styles.mutedIcon" webapp/src/components/space_settings_modal`.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd webapp && npx jest src/components/space_settings_modal`
Expected: PASS — including the pre-existing `space_settings_modal.test.tsx`, unchanged. That it still passes is the check that extracting the tab changed nothing observable. It mocks `hooks/members`, so add a `hooks/space_members` mock there only if it fails for want of one.

- [ ] **Step 8: Full check and commit**

```bash
cd webapp && npm run check-types && npx eslint src && npm test
git add webapp/src/components/space_settings_modal
git commit -m "feat(docs): rebuild settings permissions on the member core"
```

---

### Task 9: Port the space info panel

**Files:**
- Modify: `webapp/src/components/space_info/space_info_members.tsx`
- Modify: `webapp/src/components/space_info/space_info_panel.module.scss` (delete the duplicated member-row rules)
- Test: `webapp/src/components/space_info/space_info_members.test.tsx` (new file)

**Interfaces:**
- Consumes: `MemberList` from `components/space_members`.
- Produces: nothing.

The third copy, and the one that proves the core does not assume write affordances: it passes no `actions`, so no menu can render.

- [ ] **Step 1: Write the failing test**

Create `webapp/src/components/space_info/space_info_members.test.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import SpaceInfoMembers from './space_info_members';

import {renderWithContext} from '../../../tests/react_testing_utils';

const members = [
    {id: 'u1', displayName: 'Ada', username: 'ada', avatarUrl: ''},
    {id: 'u2', displayName: 'Grace', username: 'grace', avatarUrl: ''},
];

describe('SpaceInfoMembers', () => {
    it('lists the members', () => {
        renderWithContext(<SpaceInfoMembers members={members}/>);

        expect(screen.getByText('Ada')).toBeInTheDocument();
        expect(screen.getByText('@grace')).toBeInTheDocument();
    });

    // Read-only: the panel supplies no actions, so no row can offer one.
    it('offers no membership actions', () => {
        renderWithContext(<SpaceInfoMembers members={members}/>);

        expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd webapp && npx jest src/components/space_info/space_info_members.test.tsx`
Expected: PASS on the first case, FAIL on the second only if the current markup renders a button — it does not, so both may pass. That is fine: this test's job is to pin the behaviour through the rewrite. Proceed to step 3 and confirm it still passes after.

- [ ] **Step 3: Rewrite the component**

Replace the whole of `webapp/src/components/space_info/space_info_members.tsx`:

```tsx
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React from 'react';

import {MemberList} from 'components/space_members';

type Props = {
    members: MemberProfile[];
};

/**
 * The panel's members view, reached from the info menu. Mirrors core's channel
 * members RHS: the roster on its own screen rather than inline on the root.
 *
 * Read-only — it passes no actions, so the shared roster renders no row menus.
 */
const SpaceInfoMembers = ({members}: Props) => (
    <MemberList
        members={members}
        avatarSize='sm'
    />
);

export default SpaceInfoMembers;
```

- [ ] **Step 4: Delete the duplicated styles**

From `space_info_panel.module.scss`, delete `.memberList`, `.memberRow`, `.memberInfo`, `.memberName` and `.memberUsername` — but grep first, since the panel's root view may share them: `rtk proxy grep -rn "styles.memberRow\|styles.memberList\|styles.memberInfo\|styles.memberName\|styles.memberUsername" webapp/src/components/space_info`. Keep whatever another view in the panel still uses.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd webapp && npx jest src/components/space_info`
Expected: PASS, including any pre-existing panel tests.

- [ ] **Step 6: Delete the orphaned message ids**

The three per-surface duplicates are now unused. Confirm and remove any leftover definitions:

```bash
cd webapp && rtk proxy grep -rn "docs.share.handle\|docs.spaceSettings.permissions.handle\|docs.spaceInfo.handle\|docs.share.you\|docs.share.role.admin\|docs.spaceSettings.permissions.role.admin" src
```

Expected: no matches. Any hit is a surface that was not fully ported — fix it rather than leaving the id behind. Also check `i18n/en.json` if the repo keeps extracted strings there, and re-run its extraction script if one exists (`npm run` lists the scripts).

- [ ] **Step 7: Full check and commit**

```bash
cd webapp && npm run check-types && npx eslint src && npm test
git add webapp/src/components/space_info
git commit -m "feat(docs): read-only roster for the space info panel"
```

---

## Self-Review

**Spec coverage.** Every section of the spec maps to a task: data access layer → Task 2; Store → Task 1; Thunks → Task 2; Hook → Task 3; shared core → Tasks 4–6; Wrappers table → Tasks 7–9; i18n consolidation → the new ids in Tasks 4–6 plus the cleanup in Task 9 step 6; Immediacy → Task 8's constraint and its "does not mark dirty" note; Sequencing → task order; Error handling table → Task 3's tests; Testing → each task's test step.

**One deviation from the spec, deliberate.** The spec's error table says removing a member reuses `docs.leaveSpace.error.lastMember`. That string ends "…before you leave", which is wrong when you are removing somebody else, so Task 3 introduces `docs.spaceMembers.remove.error.lastMember` — "A space must keep at least one member with access." `useLeaveSpace` keeps its own wording for the case it does describe.

**One thing the implementer must verify rather than assume.** How `components/menu`'s `Menu.Item` expresses `disabled` in the DOM (`aria-disabled` vs the `disabled` attribute) — Task 5 step 5 says to check `components/menu/menu.test.tsx` for the established idiom rather than guess. Every other signature in this plan was read out of the source.

**Type consistency.** `MemberProfile` is used unchanged from `hooks/members` throughout. `MemberListActions` is defined in Task 5 and consumed by Tasks 7 and 8 under that name. `FailedMemberAdd` is defined in Task 2 and consumed only inside Task 3. `addMembers` returns `Promise<MemberProfile[]>` in Task 3 and is passed as `AddMembersField`'s `onAdd` in Task 6 with that same signature. `avatarSize` is `'sm' | 'md'` in Tasks 4–9 — the Share modal uses `'md'`, the other two `'sm'`.
