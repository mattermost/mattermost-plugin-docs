// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';
import manifest from 'manifest';

import {ClientError} from '@mattermost/client';

import {makePage, makeSpace, makeTeam} from 'store/test_fixtures';

import {DraftTypes, SpaceTypes} from './action_types';
import {addSpaceMember, addSpaceMembers, createDraft, createSpace, ensureSpaceMembership, fetchAllSpaces, fetchSpace, fetchSpaces, isLastSpaceAdminError, isLastSpaceMemberError, isNotTeamMemberError, isSpaceLockTimeoutError, leaveSpace, movePage, refreshSpaceAccess, refreshSpaceAfterMemberPermissionsChanged, refreshSpaceAfterSelfRemoval, removeSpaceMember, saveDraft, updateSpace} from './actions';

import {makeTestState} from '../../tests/react_testing_utils';

const mockAddSpaceMember = jest.fn();
const mockRemoveSpaceMember = jest.fn();
const mockMovePage = jest.fn();
const mockListPages = jest.fn();
const mockListSpaces = jest.fn();
const mockCreateSpace = jest.fn();
const mockGetSpace = jest.fn();
const mockListSpaceMembers = jest.fn();
const mockCreateSpaceDraft = jest.fn();
const mockUpdatePageDraft = jest.fn();
const mockJoinSpace = jest.fn();
const mockUpdateSpace = jest.fn();

jest.mock('data', () => ({
    docsDataSource: {
        addSpaceMember: (...args: unknown[]) => mockAddSpaceMember(...args as []),
        removeSpaceMember: (...args: unknown[]) => mockRemoveSpaceMember(...args as []),
        movePage: (...args: unknown[]) => mockMovePage(...args as []),
        listPages: (...args: unknown[]) => mockListPages(...args as []),
        listSpaces: (...args: unknown[]) => mockListSpaces(...args as []),
        createSpace: (...args: unknown[]) => mockCreateSpace(...args as []),
        getSpace: (...args: unknown[]) => mockGetSpace(...args as []),
        listSpaceMembers: (...args: unknown[]) => mockListSpaceMembers(...args as []),
        createSpaceDraft: (...args: unknown[]) => mockCreateSpaceDraft(...args as []),
        updatePageDraft: (...args: unknown[]) => mockUpdatePageDraft(...args as []),
        joinSpace: (...args: unknown[]) => mockJoinSpace(...args as []),
        updateSpace: (...args: unknown[]) => mockUpdateSpace(...args as []),
    },
}));

jest.mock('mattermost-redux/selectors/entities/users', () => ({getCurrentUserId: () => 'user1'}));

const PAGE = makePage('page1', 'space1', 'Page');

// The reducer isn't under test here; the thunks only need getState + a spy. A
// dispatched thunk runs inline, so nested dispatches (e.g. movePage's reload) work.
const run = <T>(thunk: (dispatch: jest.Mock, getState: () => unknown) => T, state: unknown = {}) => {
    const getState = () => state;
    const dispatch: jest.Mock = jest.fn((action: unknown) =>
        (typeof action === 'function' ? (action as (d: jest.Mock, g: () => unknown) => unknown)(dispatch, getState) : action));

    return {result: thunk(dispatch, getState), dispatch};
};

const stateWithPage = {
    [`plugins-${manifest.id}`]: {entities: {pages: {[PAGE.id]: PAGE}}},
};

const deferred = <T>() => {
    let resolve!: (value: T | PromiseLike<T>) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((promiseResolve, promiseReject) => {
        resolve = promiseResolve;
        reject = promiseReject;
    });

    return {promise, resolve, reject};
};

describe('fetchAllSpaces', () => {
    beforeEach(() => jest.clearAllMocks());

    it('dispatches successful team results when another team fails', async () => {
        const team1 = makeTeam('team1', 'one');
        const team2 = makeTeam('team2', 'two');
        const space = makeSpace('space1', 'One', team1.id);
        const error = new Error('no access');
        mockListSpaces.mockImplementation((teamId: string) => (
            teamId === team1.id ? Promise.resolve([space]) : Promise.reject(error)
        ));
        const consoleError = jest.spyOn(console, 'error').mockImplementation(() => {});
        const state = makeTestState({teams: [team1, team2], currentUser: {id: 'user1'}});

        const {result, dispatch} = run((d, g) => fetchAllSpaces()(d as never, g as never, undefined as never), state);
        await result;

        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: [space]});
        expect(consoleError).toHaveBeenCalledWith('Docs: failed to load spaces for team', team2.id, error);
        consoleError.mockRestore();
    });
});

describe('createSpace', () => {
    beforeEach(() => jest.clearAllMocks());

    it('rejects before calling the data source without a current team', async () => {
        const input = {title: 'New space', view_access: 'open' as const};

        const {result} = run((d, g) => createSpace(input)(d as never, g as never, undefined as never), makeTestState());

        await expect(result).rejects.toThrow('cannot create a space without a current team');
        expect(mockCreateSpace).not.toHaveBeenCalled();
    });
});

const LAST_MEMBER = 'app.space.remove_member.last_member.app_error';
const LAST_ADMIN = 'app.space.member.last_admin.app_error';
const LOCK = 'app.space.lock_timeout.app_error';

describe('409 discrimination on the removal routes', () => {
    const conflict = (id: string) =>
        new ClientError('', {message: 'nope', status_code: 409, url: '/x', server_error_id: id});

    // Three distinct rules answer 409 on these routes, so each predicate must key on the id. Keying
    // on the status made all three look like the last-member refusal, which sent a sole admin off to
    // add an ordinary member and reported a retryable lock timeout as a permanent rule violation.
    it('tells the three 409 rules apart', () => {
        expect(isLastSpaceMemberError(conflict(LAST_MEMBER))).toBe(true);
        expect(isLastSpaceAdminError(conflict(LAST_MEMBER))).toBe(false);
        expect(isSpaceLockTimeoutError(conflict(LAST_MEMBER))).toBe(false);

        expect(isLastSpaceAdminError(conflict(LAST_ADMIN))).toBe(true);
        expect(isLastSpaceMemberError(conflict(LAST_ADMIN))).toBe(false);

        expect(isSpaceLockTimeoutError(conflict(LOCK))).toBe(true);
        expect(isLastSpaceMemberError(conflict(LOCK))).toBe(false);
    });

    it('claims nothing for a 409 carrying no id, or a non-ClientError', () => {
        expect(isLastSpaceMemberError(new ClientError('', {message: 'nope', status_code: 409, url: '/x'}))).toBe(false);
        expect(isLastSpaceMemberError(new Error('boom'))).toBe(false);
        expect(isLastSpaceMemberError(undefined)).toBe(false);
    });
});

describe('leaveSpace', () => {
    beforeEach(() => jest.clearAllMocks());

    it('drops the space once the server refuses the re-read', async () => {
        mockRemoveSpaceMember.mockResolvedValue(undefined);
        mockGetSpace.mockRejectedValue(new RestError('/x', 403, 'forbidden', null));

        const {result, dispatch} = run((d, g) => leaveSpace('space1')(d as never, g as never, undefined as never));
        await result;

        expect(mockRemoveSpaceMember).toHaveBeenCalledWith('space1', 'user1');
        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.DELETED_SPACE, spaceId: 'space1'});
    });

    // This caller remains eligible for the open-space fall-through, so leaving must not evict it.
    // Dispatching DELETED_SPACE unconditionally made the space vanish from a caller who could still
    // read it, until a reload or a WebSocket event happened to put it back.
    it('keeps a space that is still readable after the caller leaves', async () => {
        const openSpace = {...makeSpace('space1', 'Open'), view_access: 'open' as const};
        mockRemoveSpaceMember.mockResolvedValue(undefined);
        mockGetSpace.mockResolvedValue(openSpace);
        mockListSpaceMembers.mockResolvedValue([]);

        const {result, dispatch} = run((d, g) => leaveSpace('space1')(d as never, g as never, undefined as never));
        await result;

        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: [openSpace]});
        expect(dispatch).not.toHaveBeenCalledWith(expect.objectContaining({type: SpaceTypes.DELETED_SPACE}));
    });

    it('rejects so the caller can explain the failure', async () => {
        const error = new ClientError('', {message: 'last member', status_code: 409, url: '/x'});
        mockRemoveSpaceMember.mockRejectedValue(error);

        const {result} = run((d, g) => leaveSpace('space1')(d as never, g as never, undefined as never));

        await expect(result).rejects.toBe(error);
    });
});

describe('movePage', () => {
    beforeEach(() => jest.clearAllMocks());

    it('reindexes optimistically, then reconciles with the server page', async () => {
        mockMovePage.mockResolvedValue({...PAGE, parent_id: 'parent1'});

        const {result, dispatch} = run((d, g) => movePage('space1', PAGE.id, 'parent1', 0)(d as never, g as never, undefined as never), stateWithPage);
        await result;

        expect(dispatch.mock.calls[0][0]).toEqual(expect.objectContaining({pageId: PAGE.id, parentId: 'parent1', siblingIndex: 0}));
        expect(mockMovePage).toHaveBeenCalledWith('space1', PAGE.id, 'parent1', 0, PAGE.update_at);
    });

    // The optimistic reindex is wrong once the server refuses, so the list is
    // reloaded and the failure still reaches the caller to surface.
    it('restores server truth and rejects when the move fails', async () => {
        const error = new Error('boom');
        mockMovePage.mockRejectedValue(error);
        mockListPages.mockResolvedValue([PAGE]);

        const {result} = run((d, g) => movePage('space1', PAGE.id, 'parent1', 0)(d as never, g as never, undefined as never), stateWithPage);

        await expect(result).rejects.toBe(error);
        expect(mockListPages).toHaveBeenCalledWith('space1');
    });

    it('does nothing for a page that is not loaded', async () => {
        const {result} = run((d, g) => movePage('space1', 'ghost', '', 0)(d as never, g as never, undefined as never), stateWithPage);
        await result;

        expect(mockMovePage).not.toHaveBeenCalled();
    });
});

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
    it('recognises the not-team-member id and nothing else', () => {
        expect(isNotTeamMemberError(new ClientError('', {
            message: 'nope',
            status_code: 403,
            url: '/x',
            server_error_id: 'app.space.member.not_team_member.app_error',
        }))).toBe(true);

        // A bare 403 (e.g. a non-manage caller) carries a different id and must fall
        // to the generic message, not the not-team-member one.
        expect(isNotTeamMemberError(new ClientError('', {message: 'nope', status_code: 403, url: '/x'}))).toBe(false);
        expect(isNotTeamMemberError(new Error('boom'))).toBe(false);
    });
});

describe('refreshSpaceAfterSelfRemoval', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        jest.spyOn(console, 'error').mockImplementation(() => {});
    });
    afterEach(() => jest.restoreAllMocks());

    const space = makeSpace('space1', 'One');
    const removed = {type: SpaceTypes.DELETED_SPACE, spaceId: 'space1'};

    // The server may still admit this caller through the open-space fall-through, so the re-read
    // decides whether removal narrowed or ended access.
    it('keeps a space the server still serves, and refreshes its roster', async () => {
        mockGetSpace.mockResolvedValue(space);
        mockListSpaceMembers.mockResolvedValue([{user_id: 'other'}]);

        const {result, dispatch} = run((d, g) => refreshSpaceAfterSelfRemoval('space1')(d as never, g as never, undefined as never));
        await result;

        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: [space]});
        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACE_MEMBERS, spaceId: 'space1', userIds: ['other']});
        expect(dispatch).not.toHaveBeenCalledWith(removed);
    });

    it.each([403, 404])('evicts the space when the server answers %i', async (status) => {
        mockGetSpace.mockRejectedValue(new RestError('/spaces/space1', status, 'denied', undefined));

        const {result, dispatch} = run((d, g) => refreshSpaceAfterSelfRemoval('space1')(d as never, g as never, undefined as never));
        await result;

        expect(dispatch).toHaveBeenCalledWith(removed);
    });

    // "The request did not complete" is not an answer about access. Evicting on it would drop a
    // space the caller can still read until some later listing happened to bring it back.
    it('leaves the space standing when the read fails without a verdict', async () => {
        mockGetSpace.mockRejectedValue(new Error('network down'));

        const {result, dispatch} = run((d, g) => refreshSpaceAfterSelfRemoval('space1')(d as never, g as never, undefined as never));
        await result;

        expect(dispatch).not.toHaveBeenCalledWith(removed);
    });

    it('leaves the space standing on a server fault', async () => {
        mockGetSpace.mockRejectedValue(new RestError('/spaces/space1', 500, 'boom', undefined));

        const {result, dispatch} = run((d, g) => refreshSpaceAfterSelfRemoval('space1')(d as never, g as never, undefined as never));
        await result;

        expect(dispatch).not.toHaveBeenCalledWith(removed);
    });
});

describe('refreshSpaceAfterMemberPermissionsChanged', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        jest.spyOn(console, 'error').mockImplementation(() => {});
    });
    afterEach(() => jest.restoreAllMocks());

    it('stores fresh access before invalidating the local grant matrix', async () => {
        const space = makeSpace('space1', 'One');
        mockGetSpace.mockResolvedValue(space);

        const {result, dispatch} = run((d, g) => refreshSpaceAfterMemberPermissionsChanged('space1')(d as never, g as never, undefined as never));
        await result;

        const actions = dispatch.mock.calls.
            map(([action]) => action).
            filter((action) => typeof action !== 'function');
        expect(actions).toEqual([
            {type: SpaceTypes.RECEIVED_SPACES, spaces: [space]},
            {type: SpaceTypes.SPACE_MEMBER_PERMISSIONS_CHANGED, spaceId: 'space1'},
        ]);
    });

    it('does not invalidate grants when access could not be refreshed', async () => {
        mockGetSpace.mockRejectedValue(new Error('network down'));

        const {result, dispatch} = run((d, g) => refreshSpaceAfterMemberPermissionsChanged('space1')(d as never, g as never, undefined as never));
        await result;

        expect(dispatch).not.toHaveBeenCalledWith({type: SpaceTypes.SPACE_MEMBER_PERMISSIONS_CHANGED, spaceId: 'space1'});
    });

    // The event that triggered this refresh may be the caller's own revocation, in which case the
    // re-read is the denial: the stale privileged record must not keep rendering admin affordances.
    it('evicts the space when the refresh answers a definitive denial', async () => {
        mockGetSpace.mockRejectedValue(new RestError('/spaces/space1', 403, 'denied', undefined));

        const {result, dispatch} = run((d, g) => refreshSpaceAfterMemberPermissionsChanged('space1')(d as never, g as never, undefined as never));
        await result;

        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.DELETED_SPACE, spaceId: 'space1'});
        expect(dispatch).not.toHaveBeenCalledWith({type: SpaceTypes.SPACE_MEMBER_PERMISSIONS_CHANGED, spaceId: 'space1'});
    });
});

// The `spaces` slice has a dozen independent async writers — thunks, hooks, WebSocket handlers —
// and the reducer applies whatever arrives. The issue-order guard is what keeps a slower request
// issued earlier from silently reverting the state a fresher response wrote.
describe('space access ordering', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        jest.spyOn(console, 'error').mockImplementation(() => {});
    });
    afterEach(() => jest.restoreAllMocks());

    it('a slower earlier read does not overwrite a fresher response', async () => {
        const stale = makeSpace('space1', 'Stale');
        const fresh = makeSpace('space1', 'Fresh');
        const first = deferred<unknown>();
        mockGetSpace.mockReturnValueOnce(first.promise).mockResolvedValueOnce(fresh);

        const earlier = run((d, g) => refreshSpaceAccess('space1')(d as never, g as never, undefined as never));
        const later = run((d, g) => refreshSpaceAccess('space1')(d as never, g as never, undefined as never));
        await later.result;
        first.resolve(stale);
        await earlier.result;

        expect(later.dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: [fresh]});
        expect(earlier.dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: []});
        expect(earlier.dispatch).not.toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: [stale]});
    });

    // A team listing carries bare spaces, so an earlier-issued one landing late must not regress the
    // record a fresher single-space response already wrote. view_access and update_at are the fields
    // at stake: the reducer's own merge only rescues the three access fields, and update_at is the
    // optimistic-lock baseline every later write reads, so a regressed one turns the next edit into a
    // spurious conflict on an uncontended space.
    it('a slower earlier team listing does not regress a fresher space record', async () => {
        const team = makeTeam('team1', 'one');
        const fresh = {...makeSpace('space1', 'Fresh', team.id), view_access: 'private' as const, update_at: 200};
        const stale = {...makeSpace('space1', 'Stale', team.id), view_access: 'open' as const, update_at: 100};
        const other = makeSpace('space2', 'Other', team.id);
        const listing = deferred<unknown>();
        mockListSpaces.mockReturnValueOnce(listing.promise);
        mockGetSpace.mockResolvedValueOnce(fresh);
        const state = makeTestState({
            currentTeam: team,
            currentUser: {id: 'user1'},
            docs: {spaces: {[fresh.id]: fresh}},
        });

        // The listing is issued first, so it claims the earlier slot; the single-space read is issued
        // and applied while it is still in flight.
        const earlier = run((d, g) => fetchSpaces()(d as never, g as never, undefined as never), state);
        await run((d, g) => refreshSpaceAccess('space1')(d as never, g as never, undefined as never), state).result;
        listing.resolve([stale, other]);
        await earlier.result;

        // space1 keeps the fresher record, and is still present so the team index does not lose it.
        expect(earlier.dispatch).toHaveBeenCalledWith({
            type: SpaceTypes.RECEIVED_SPACES,
            spaces: [fresh, other],
            teamId: team.id,
        });
    });

    // The eviction removed the record, so there is nothing to replace the superseded listing entry
    // with. Carrying the listing's own bare space instead put a space the server had just refused
    // back into the team index, and its metadata back on screen, until the next listing.
    it('omits a space evicted while the team listing was in flight', async () => {
        const team = makeTeam('team1', 'one');
        const denied = makeSpace('space1', 'Denied', team.id);
        const other = makeSpace('space2', 'Other', team.id);
        const listing = deferred<unknown>();
        mockListSpaces.mockReturnValueOnce(listing.promise);
        mockGetSpace.mockRejectedValueOnce(new RestError('/spaces/space1', 403, 'denied', undefined));

        // The store no longer holds space1, because the denial below has already evicted it.
        const state = makeTestState({currentTeam: team, currentUser: {id: 'user1'}, docs: {spaces: {}}});

        const earlier = run((d, g) => fetchSpaces()(d as never, g as never, undefined as never), state);
        await run((d, g) => refreshSpaceAccess('space1')(d as never, g as never, undefined as never), state).result;
        listing.resolve([denied, other]);
        await earlier.result;

        expect(earlier.dispatch).toHaveBeenCalledWith({
            type: SpaceTypes.RECEIVED_SPACES,
            spaces: [other],
            teamId: team.id,
        });
    });

    // A read issued after a write can still return before it, having read the pre-write record.
    // Ordering the write by issue discarded it, leaving the old update_at in the store, so the next
    // edit sent a stale optimistic-lock baseline and failed as a conflict on an uncontended space.
    it('applies a successful update that a later read overtook', async () => {
        const stored = {...makeSpace('space1', 'Stored'), update_at: 100};
        const updated = {...makeSpace('space1', 'Renamed'), update_at: 200};
        const write = deferred<unknown>();
        mockUpdateSpace.mockReturnValueOnce(write.promise);
        mockGetSpace.mockResolvedValueOnce(stored);
        const state = makeTestState({currentUser: {id: 'user1'}, docs: {spaces: {[stored.id]: stored}}});

        const update = run((d, g) => updateSpace('space1', {title: 'Renamed'})(d as never, g as never, undefined as never), state);
        await run((d, g) => refreshSpaceAccess('space1')(d as never, g as never, undefined as never), state).result;
        write.resolve(updated);
        await update.result;

        expect(update.dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: [updated]});
    });

    // Completion ordering must not reach past an eviction: the server may have committed the write
    // before it refused the read that evicted, so the refusal is the later word on access.
    it('does not restore a space evicted while the update was in flight', async () => {
        const stored = {...makeSpace('space1', 'Stored'), update_at: 100};
        const updated = {...makeSpace('space1', 'Renamed'), update_at: 200};
        const write = deferred<unknown>();
        mockUpdateSpace.mockReturnValueOnce(write.promise);
        mockGetSpace.mockRejectedValueOnce(new RestError('/spaces/space1', 403, 'denied', undefined));
        const state = makeTestState({currentUser: {id: 'user1'}, docs: {spaces: {[stored.id]: stored}}});

        const update = run((d, g) => updateSpace('space1', {title: 'Renamed'})(d as never, g as never, undefined as never), state);
        await run((d, g) => refreshSpaceAccess('space1')(d as never, g as never, undefined as never), state).result;
        write.resolve(updated);
        await update.result;

        expect(update.dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: []});
    });

    // fetchSpace is the reconciliation path for access changes no event announces: an open space
    // turned private drops the non-member's channel WebSocket. Ignoring its denial left the record,
    // and every affordance gated on its permissions, in place until a full reload.
    it('evicts the space when a plain read answers a definitive denial', async () => {
        for (const status of [403, 404]) {
            mockGetSpace.mockRejectedValueOnce(new RestError('/spaces/space1', status, 'denied', undefined));

            // eslint-disable-next-line no-await-in-loop
            const {result, dispatch} = run((d, g) => fetchSpace('space1')(d as never, g as never, undefined as never));
            // eslint-disable-next-line no-await-in-loop
            expect(await result).toBeUndefined();
            expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.DELETED_SPACE, spaceId: 'space1'});
        }
    });

    // A request that never completed is not an answer about access, so the stored record stands.
    it('retains the space when a plain read fails transiently', async () => {
        mockGetSpace.mockRejectedValue(new Error('network down'));

        const {result, dispatch} = run((d, g) => fetchSpace('space1')(d as never, g as never, undefined as never));
        expect(await result).toBeUndefined();
        expect(dispatch).not.toHaveBeenCalledWith({type: SpaceTypes.DELETED_SPACE, spaceId: 'space1'});
    });

    it('an eviction supersedes a read still in flight', async () => {
        const pending = deferred<unknown>();
        mockGetSpace.
            mockReturnValueOnce(pending.promise).
            mockRejectedValueOnce(new RestError('/spaces/space1', 403, 'denied', undefined));

        const read = run((d, g) => fetchSpace('space1')(d as never, g as never, undefined as never));
        const denial = run((d, g) => refreshSpaceAccess('space1')(d as never, g as never, undefined as never));
        await denial.result;
        pending.resolve(makeSpace('space1', 'Stale'));
        await read.result;

        expect(denial.dispatch).toHaveBeenCalledWith({type: SpaceTypes.DELETED_SPACE, spaceId: 'space1'});
        expect(read.dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: []});
    });
});

// Where the join sits decides what a membership means. Joining when the editor opens would record
// an intention — someone who clicks Edit, types nothing and navigates away would be a member for
// good, and a removed user would rejoin by opening an editor rather than by writing. Joining on the
// write keeps it a record of a contribution.
describe('ensureSpaceMembership', () => {
    beforeEach(() => jest.clearAllMocks());

    const joinableState = (canJoin: boolean) => makeTestState({
        docs: {spaces: {space1: {...makeSpace('space1', 'Open'), can_join: canJoin}}},
        currentUser: {id: 'user1'},
    });

    it('joins a space the server said the caller may join', async () => {
        mockJoinSpace.mockResolvedValue({...makeSpace('space1', 'Open'), can_join: false});

        const {result} = run((d, g) => ensureSpaceMembership('space1')(d as never, g as never, undefined as never), joinableState(true));
        await result;

        expect(mockJoinSpace).toHaveBeenCalledWith('space1');
    });

    // Every case but a non-member of an open space, which is the overwhelming majority of calls:
    // this runs on every autosave, so a member must not make a network request per keystroke batch.
    it('does not call the server when there is nothing to join', async () => {
        const {result} = run((d, g) => ensureSpaceMembership('space1')(d as never, g as never, undefined as never), joinableState(false));
        await result;

        expect(mockJoinSpace).not.toHaveBeenCalled();
    });

    it('awaits membership before creating a draft, then reconciles the store', async () => {
        const join = deferred<ReturnType<typeof makeSpace> & {can_join: boolean}>();
        const draft = {page_id: 'page1'};
        mockJoinSpace.mockReturnValue(join.promise);
        mockCreateSpaceDraft.mockResolvedValue(draft);

        const {result, dispatch} = run((d, g) => createDraft('space1', 'Title')(d as never, g as never, undefined as never), joinableState(true));

        expect(mockJoinSpace).toHaveBeenCalledWith('space1');
        expect(mockCreateSpaceDraft).not.toHaveBeenCalled();

        join.resolve({...makeSpace('space1', 'Open'), can_join: false});
        await expect(result).resolves.toBe(draft);

        expect(mockCreateSpaceDraft).toHaveBeenCalledWith('space1', 'Title', '');
        expect(dispatch).toHaveBeenCalledWith({type: DraftTypes.RECEIVED_DRAFT, draft});
    });

    it('does not create a draft when joining fails', async () => {
        const error = new Error('join failed');
        const join = deferred<ReturnType<typeof makeSpace> & {can_join: boolean}>();
        mockJoinSpace.mockReturnValue(join.promise);

        const {result, dispatch} = run((d, g) => createDraft('space1', 'Title')(d as never, g as never, undefined as never), joinableState(true));

        expect(mockCreateSpaceDraft).not.toHaveBeenCalled();
        const rejection = expect(result).rejects.toBe(error);
        join.reject(error);
        await rejection;

        expect(mockCreateSpaceDraft).not.toHaveBeenCalled();
        expect(dispatch).not.toHaveBeenCalledWith(expect.objectContaining({type: DraftTypes.RECEIVED_DRAFT}));
    });

    it('awaits membership before saving a draft, then reconciles the store', async () => {
        const join = deferred<ReturnType<typeof makeSpace> & {can_join: boolean}>();
        const draft = {page_id: 'page1'};
        mockJoinSpace.mockReturnValue(join.promise);
        mockUpdatePageDraft.mockResolvedValue(draft);

        const {result, dispatch} = run((d, g) => saveDraft('space1', 'page1', {body: 'hi'})(d as never, g as never, undefined as never), joinableState(true));

        expect(mockJoinSpace).toHaveBeenCalledWith('space1');
        expect(mockUpdatePageDraft).not.toHaveBeenCalled();

        join.resolve({...makeSpace('space1', 'Open'), can_join: false});
        await expect(result).resolves.toBe(draft);

        expect(mockUpdatePageDraft).toHaveBeenCalledWith('space1', 'page1', {body: 'hi'}, undefined);
        expect(dispatch).toHaveBeenCalledWith({type: DraftTypes.RECEIVED_DRAFT, draft});
    });

    it('does not save a draft when joining fails', async () => {
        const error = new Error('join failed');
        const join = deferred<ReturnType<typeof makeSpace> & {can_join: boolean}>();
        mockJoinSpace.mockReturnValue(join.promise);

        const {result, dispatch} = run((d, g) => saveDraft('space1', 'page1', {body: 'hi'})(d as never, g as never, undefined as never), joinableState(true));

        expect(mockUpdatePageDraft).not.toHaveBeenCalled();
        const rejection = expect(result).rejects.toBe(error);
        join.reject(error);
        await rejection;

        expect(mockUpdatePageDraft).not.toHaveBeenCalled();
        expect(dispatch).not.toHaveBeenCalledWith(expect.objectContaining({type: DraftTypes.RECEIVED_DRAFT}));
    });
});
