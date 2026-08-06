# Share modal: add and remove space members

Date: 2026-08-06
Status: approved, ready for implementation planning

## Problem

`POST /api/v1/spaces/{space_id}/members` and `DELETE /api/v1/spaces/{space_id}/members/{user_id}`
have existed on the server since the space-membership work landed. Nothing in the webapp calls the
add route, and the delete route is reached only by `leaveSpace`, which removes the current user.

The consequence is a Share modal that looks finished and isn't. `PeoplePicker` searches the team,
accumulates chips, and drops them on the floor: `canManageMembers` is already forced `true` by the
dev override in `store/permissions.ts`, so the control ships, but a selection never becomes a
member. Member rows carry an `Admin ▾` trigger that is inert, and there is no way to remove anyone.
Two comments in `share_space_modal.tsx` state that the add-member API does not exist yet and is
waiting on PR #10; both are wrong, and they are the reason the wiring was never done.

This spec covers only membership: adding people, removing people, and leaving. Roles and space
view-access remain scaffolding for PR #10 (MM-69269).

## Scope

In scope:

- `addSpaceMember` on the data source, over the existing POST route.
- Store action types and reducer cases for adding and removing one member.
- A singular thunk for each mutation, plus a separate bulk add thunk composed from the singular one.
- A hook that owns the toasts and the busy state.
- An `Add` button in the Share modal, and a per-row menu offering Remove (or Leave on your own row).
- Correcting the two stale comments.

Out of scope:

- Per-member roles and the role dropdown's behaviour (PR #10 capabilities).
- Space view-access — the `Public` / `Can View` footer controls (PR #10 `view_access`).
- Flipping `canManageMembers`. It is already `true` via the dev override, and that override's
  comment stays accurate as the thing to revisit when real capabilities arrive.
- `PeoplePicker` and its stylesheet. The multi-select chip UI it already has is exactly what this
  flow needs; it is not touched.
- Bulk remove. The bulk add thunk establishes the composition pattern; a bulk remove can follow the
  same shape if a surface ever needs it.
- The space-info panel's member list. The hook is designed so that surface can reuse it later, but
  this change does not touch it.

## Server contract, as it exists today

`POST /api/v1/spaces/{space_id}/members`, body `{"user_id": "..."}`, returns `201` with
`{"user_id": "..."}`.

- The **acting** user must pass `requireSpaceMembership` — backing-channel membership plus active
  team membership. Only an existing space member can add anyone; there is no self-serve join.
- The **target** must be an active member of the space's team, else `403`
  `app.space.member.not_team_member.app_error`.
- An unknown target user is `404` `app.space.member.user_not_found.app_error`.
- Publishes `space_member_added` to the backing channel.

`DELETE /api/v1/spaces/{space_id}/members/{user_id}` returns `200`.

- Same gate on the acting user.
- Removing the last member who can still reach the space is refused with `409`
  `app.space.remove_member.last_member.app_error`.
- Publishes `space_member_removed` to the backing channel and directly to the removed user.

There is no bulk route. Adding N people is N requests, which is why the fan-out belongs on the
client and why the singular call is the primitive.

Behaviour when the target is **already a member** is core's, not this plugin's: `AddSpaceMember`
does not pre-check, it calls `pluginapi`'s `Channel.AddMember` straight through. Whether that is
idempotent or an error has not been verified. `excludeIds` already filters current members out of
the suggestion list, so this is only reachable when someone else adds the same person mid-flight;
it falls to the generic error path rather than getting special handling.

## Design

### Data access layer

`data/docs_data_source.ts`, one method beside the existing `removeSpaceMember`:

```ts
// Adds a user to a space. The server requires the target to be an active member
// of the space's team (403 otherwise) and rejects an unknown user (404).
addSpaceMember(spaceId: string, userId: string): Promise<SpaceMember>;
```

`data/api_data_source.ts`:

```ts
addSpaceMember: (spaceId, userId) =>
    restPost<SpaceMember>(`${apiUrl()}/spaces/${seg(spaceId)}/members`, {user_id: userId}),
```

One method, singular, mirroring the route. No bulk method: the transport has nothing to batch, so a
bulk data-source method would only be a loop wearing a transport's clothes.

`removeSpaceMember` is unchanged; it already maps to the DELETE route.

### Store

`store/action_types.ts`, two additions to `SpaceTypes`:

```ts
ADDED_SPACE_MEMBER:   manifest.id + '_added_space_member',
REMOVED_SPACE_MEMBER: manifest.id + '_removed_space_member',
```

Two action types, not three. A bulk add produces N `ADDED_SPACE_MEMBER` dispatches, one per member
that actually landed — there is no bulk action type, because a partially-successful batch has no
single truth to express.

`store/entities.ts`, the `spaceMembers` reducer (`Record<string, string[]>`) handles both by
splicing the space's id array. Two invariants:

- Return `state` unchanged when the id is already present (add) or already absent (remove), so a
  no-op dispatch does not produce a new object and re-render every consumer.
- Do **not** seed a space's entry on add. An absent entry means "members not loaded", which
  `areMembersLoadedForSpace` relies on to tell that apart from "no members"; seeding on a mutation
  would claim the list is loaded when only one id is known. If the entry is absent, add is a no-op
  on the index — `fetchSpaceMembers` is what populates it.

  This is safe for the Share modal specifically: it is reachable only from `space_header` inside
  `SpaceView`, which calls `useSpaceStats` on mount, which dispatches `fetchSpaceMembers`. So the
  entry exists by the time the modal can be opened, and the splice lands. If a future surface opens
  the modal without that fetch having run, it must dispatch `fetchSpaceMembers` itself — the
  reducer will not invent a list.

Rejected alternative: refetching `listSpaceMembers` after each mutation. It needs no reducer
change, but `RECEIVED_SPACE_MEMBERS` replaces the whole array, so a concurrent change clobbers
rather than merges. The splice is the narrower write, and it costs no extra round trip.

Also rejected: optimistic splice with rollback. More code, and a rollback path to get wrong, for an
action nobody performs in a loop.

### Thunks

`store/actions.ts`. Three thunks, in two layers.

**Singular — the primitives.** Each awaits the data source, dispatches its action on success, and
**rejects** on failure, matching `createPage` and `deleteSpace`. They know nothing about batching:
no partial success, no result objects, no swallowed errors.

```ts
addSpaceMember(spaceId: string, userId: string): Promise<void>
removeSpaceMember(spaceId: string, userId: string): Promise<void>
```

**Bulk — composed from the primitive.**

```ts
export type FailedMemberAdd = {userId: string; error: unknown};

// Adds several members by dispatching addSpaceMember once per user, concurrently.
// Never rejects: a batch has no single outcome, so the result is the list of users
// that failed and why. An empty list means every add landed.
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

The shape matters more than the code: `addSpaceMembers` is a *caller* of `addSpaceMember`, not a
generalisation of it. The primitive keeps rejecting, so a single add reads naturally at any call
site; the wrapper absorbs `allSettled` and hands back per-user outcomes. Each successful add has
already dispatched by the time the wrapper resolves, so the store is correct even for a batch that
partly failed.

Returning `error` rather than a pre-formatted message keeps message selection in the hook, where
`isLastSpaceMemberError`-style classification already lives.

`leaveSpace` stays as it is. It is a different operation — it dispatches `DELETED_SPACE`, dropping
the space from the store entirely, because the current user losing access means the space should
leave their sidebar. The new `removeSpaceMember` thunk only edits the member array. Both get a
comment pointing at the other, since the names are one word apart and confusing them would either
strip a space from everyone's store or leave a departed user's own store stale.

### Hook

`hooks/space_members.ts`, new:

```ts
useManageSpaceMembers(space: Space): {
    // Returns the users that FAILED, so the caller can keep exactly those chips.
    addMembers: (users: MemberProfile[]) => Promise<MemberProfile[]>;
    removeMember: (userId: string) => Promise<void>;
    leave: () => Promise<void>;
    busy: boolean;
}
```

- `addMembers` dispatches `addSpaceMembers`, then maps the returned `userId`s back to the
  `MemberProfile`s it was handed so the modal can restore exactly those chips. It raises the toast.
- `removeMember` dispatches the singular thunk and catches; it distinguishes the 409 via the
  existing `isLastSpaceMemberError`.
- `leave` delegates to `useLeaveSpace(space)`, inheriting its last-member message and its
  navigate-home behaviour rather than restating either.
- `busy` is true while any mutation is in flight. It disables the Add button and the menus' action
  item (Remove / Leave). It does **not** disable the menu trigger — the menu still opens, so the
  role items remain readable and the disabled action is visibly the thing that is unavailable.
- Toasts are raised here, not in the component, so the space-info panel can adopt the hook without
  duplicating the error vocabulary.

The hook is the only layer that knows about both profiles and messages. The thunks deal in ids and
errors; the component deals in chips.

### Components

`components/share_space_modal/share_space_modal.tsx`:

- An `Add` button below the picker, enabled when `pending.length > 0 && !busy`. On click it calls
  `addMembers(pending)` and sets `pending` to the returned failures — landed chips disappear,
  failed ones stay visible next to the toast that explains them.
- `excludeIds` keeps its current union of member ids and pending selections, so neither a current
  member nor an already-picked person appears in the suggestions.
- The two stale comments about there being no add-member API are removed. The remaining comment
  narrows to what is still true: roles and view-access are PR #10 scaffolding.

`components/share_space_modal/member_row_menu.tsx`, new. Wraps the row's existing `Admin ▾` trigger
in `components/menu`:

- Role items (`Admin`, `Can edit`, `Can view`) render with `disabled`, carrying a comment that they
  light up with PR #10's capabilities. They are shown rather than hidden so the menu does not
  visibly change shape when that lands.
- `Menu.Separator`, then the action: `Remove from space` with `destructive`, or `Leave space` on the
  current user's own row.
- Leaving closes the modal on success; `useLeaveSpace` handles the navigation.

The role labels currently sit inline in the modal's row markup. Moving them into this component is
the only restructuring in this change, and it keeps the row from taking on a third responsibility
alongside layout and identity display.

## Error handling

| Case | Surface |
| --- | --- |
| Add, one failure: target not an active team member (403) | Toast: "{name} isn't a member of this team." |
| Add, one failure: anything else | Toast: "Couldn't add {name}. Please try again." |
| Add, several failures | One toast naming the count; failed chips stay in the picker |
| Add, all succeeded | No toast; the rows appearing is the confirmation |
| Remove: last member with access (409) | Toast reusing the existing `docs.leaveSpace.error.lastMember` string |
| Remove: anything else | Toast: generic failure |
| Leave | Inherited from `useLeaveSpace` — its own 409 message, navigate home |

A batch with exactly one failure still names that user and its reason — the per-user `error` in the
bulk result makes that free, and it is the common case when someone picks a few people and one of
them turns out not to be on the team. Only a genuinely multi-failure batch collapses to a count:
stacking N toasts for one click is worse than one that says how many did not land, and the failed
chips staying in the picker is what identifies *which*.

Distinguishing the 403 by message is worth the effort because it is the one failure the user can act
on: the person they picked has to join the team first. Everything else is either a race or a server
fault, where a generic message is honest and a specific one would be guesswork.

## Testing

- `store/entities.test.ts` — `ADDED_SPACE_MEMBER` appends; `REMOVED_SPACE_MEMBER` removes; each
  returns the identical state object for a no-op; add does not seed an absent space entry.
- `store/actions.test.ts` — the singular thunks dispatch the right action on success and reject on
  failure; `addSpaceMembers` resolves (never rejects) with only the failed users, and the successes
  in a mixed batch have still dispatched. Uses the existing `jest.mock('data')` harness.
- `hooks/space_members.test.tsx` — `addMembers` maps failed ids back to the profiles it was given; a
  single 403 names the user while a multi-failure batch reports a count; a 409 from `removeMember`
  produces the last-member message rather than the generic one; `leave` delegates to `useLeaveSpace`.
- `components/share_space_modal/share_space_modal.test.tsx` — Add is disabled with no chips and while
  busy; clicking it calls the hook with the pending users; failed users remain as chips and
  successful ones do not; a row for another user offers Remove; the current user's row offers Leave.

## Consequences

Adding a member writes real backing-channel membership. Under the current access model that
membership is also what makes the space visible in the team listing, so adding someone is
functionally granting them access — which is the intent. When PR #10 introduces `private` spaces,
the same membership becomes the per-space grant, so the semantics carry over rather than needing
migration.

A batch is N requests against one backing channel, issued concurrently. That is acceptable at the
scale a person can pick from a combobox. If a future surface adds many members at once — an
import, a group expansion — the fan-out is one function to change, and the primitive underneath it
does not move.

WebSocket events for both mutations are published server-side but the webapp registers no handlers
yet, so a second client sees the change on its next fetch. That gap is not addressed here.
