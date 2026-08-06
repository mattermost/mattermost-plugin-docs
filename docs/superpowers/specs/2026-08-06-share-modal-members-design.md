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
- Thunks for both, plus a hook that owns the toasts and the busy state.
- Adding on select in the Share modal's picker, and a per-row menu offering Remove (or Leave on
  your own row).
- Reducing `PeoplePicker` from multi-select-with-chips to single-select.
- Correcting the two stale comments.

Out of scope:

- Per-member roles and the role dropdown's behaviour (PR #10 capabilities).
- Space view-access — the `Public` / `Can View` footer controls (PR #10 `view_access`).
- Flipping `canManageMembers`. It is already `true` via the dev override, and that override's
  comment stays accurate as the thing to revisit when real capabilities arrive.
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

`removeSpaceMember` is unchanged; it already maps to the DELETE route.

### Store

`store/action_types.ts`, two additions to `SpaceTypes`:

```ts
ADDED_SPACE_MEMBER:   manifest.id + '_added_space_member',
REMOVED_SPACE_MEMBER: manifest.id + '_removed_space_member',
```

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

`store/actions.ts`:

```ts
addSpaceMember(spaceId: string, userId: string): Promise<void>
removeSpaceMember(spaceId: string, userId: string): Promise<void>
```

Each awaits the data source, then dispatches its action on success. Both **reject** rather than
swallowing, matching `createPage` and `deleteSpace`, because the caller has to tell a 403 from a
last-member 409 to say anything useful.

`leaveSpace` stays as it is. It is a different operation — it dispatches `DELETED_SPACE`, dropping
the space from the store entirely, because the current user losing access means the space should
leave their sidebar. The new `removeSpaceMember` thunk only edits the member array. Both get a
comment pointing at the other, since the names are one word apart and confusing them would either
strip a space from everyone's store or leave a departed user's own store stale.

### Hook

`hooks/space_members.ts`, new:

```ts
useManageSpaceMembers(space: Space): {
    addMember: (user: MemberProfile) => Promise<void>;
    removeMember: (userId: string) => Promise<void>;
    leave: () => Promise<void>;
    busy: boolean;
}
```

- `addMember` adds one user. One member is the whole action: it is the unit the server route accepts
  and the unit the reducer splices, so nothing about it is a fragment of something larger. A bulk
  operation can be layered on later as a wrapper over this primitive — `allSettled` over N calls,
  plus whatever partial-failure reporting that surface wants — without this signature changing.
  Because it is whole, its failure is whole too: one user, one reason, one toast.
- `removeMember` is a single call; it distinguishes the 409 via the existing `isLastSpaceMemberError`.
- `leave` delegates to `useLeaveSpace(space)`, inheriting its last-member message and its
  navigate-home behaviour rather than restating either.
- `busy` is true while a remove or leave is in flight, and disables the menus' action item
  (Remove / Leave) so it cannot be double-fired. It does **not** disable the menu trigger — the menu
  still opens, so the role items remain readable and the disabled action is visibly the thing that is
  unavailable. It does **not** gate the picker: selections are independent, so picking two people in
  quick succession issues two independent adds that resolve on their own.
- Toasts are raised here, not in the component, so the space-info panel can adopt the hook without
  duplicating the error vocabulary.

### Components

`components/share_space_modal/share_space_modal.tsx`:

- Picking a person calls `addMember(user)` directly. There is no Add button and no `pending` state —
  both are deleted. The member list is the feedback: on success the row appears, on failure a toast
  explains why and nothing changed.
- `excludeIds` reduces to the current member ids. It no longer has to union in pending selections,
  because a successful add makes that person a member, which excludes them from suggestions on the
  next render.
- The two stale comments about there being no add-member API are removed. The remaining comment
  narrows to what is still true: roles and view-access are PR #10 scaffolding.

`components/share_space_modal/people_picker.tsx` drops to single-select:

```ts
type Props = {
    excludeIds: string[];
    onSelect: (user: MemberProfile) => void;
};
```

`Combobox.Root` loses `multiple`, and `Combobox.Chips` / `Chip` / `ChipRemove` go with it — with no
pending set to render there is nothing for a chip to represent. On select the handler fires
`onSelect` and clears the query so the field is ready for the next person. Base UI's Combobox holds
the selected value, so the component keeps `value={null}` and treats selection as an event rather
than state; that reset is the one implementation detail worth verifying against Base UI's controlled
behaviour during the build. `people_picker.module.scss` loses its chip rules.

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
| Add: target not an active team member (403) | Toast: "{name} isn't a member of this team." |
| Add: unknown user (404), or anything else | Toast: "Couldn't add {name}. Please try again." |
| Remove: last member with access (409) | Toast reusing the existing `docs.leaveSpace.error.lastMember` string |
| Remove: anything else | Toast: generic failure |
| Leave | Inherited from `useLeaveSpace` — its own 409 message, navigate home |

Every add failure can name its user, because an add is always one user. Distinguishing the 403 by
message on top of that is worth the effort because it is the one failure the user can act on: the
person they picked has to join the team first. Everything else is either a race or a server fault,
where a generic message is honest and a specific one would be guesswork.

## Testing

- `store/entities.test.ts` — `ADDED_SPACE_MEMBER` appends; `REMOVED_SPACE_MEMBER` removes; each
  returns the identical state object for a no-op; add does not seed an absent space entry.
- `store/actions.test.ts` — both thunks dispatch the right action on success and reject on failure,
  through the existing `jest.mock('data')` harness.
- `hooks/space_members.test.tsx` — `addMember` dispatches on success; a 403 names the user and a
  409 from `removeMember` produces the last-member message rather than the generic one; `leave`
  delegates to `useLeaveSpace`.
- `components/share_space_modal/share_space_modal.test.tsx` — selecting a person calls `addMember`
  with that user; a failed add leaves the member list unchanged; a row for another user offers
  Remove; the current user's row offers Leave.
- `components/share_space_modal/people_picker.test.tsx` — selecting fires `onSelect` once with the
  picked profile and clears the query; existing members are absent from the suggestions.

## Consequences

Adding on select means there is no review step: a mis-click grants access immediately, and the only
correction is Remove. That is the accepted trade for a whole, simple action — an Add button would
have bought a moment to reconsider at the cost of pending state, batch semantics and partial-failure
reporting. Remove is one menu item away on the row that just appeared, so the mistake is cheap to
undo.

Adding a member writes real backing-channel membership. Under the current access model that
membership is also what makes the space visible in the team listing, so adding someone is
functionally granting them access — which is the intent. When PR #10 introduces `private` spaces,
the same membership becomes the per-space grant, so the semantics carry over rather than needing
migration.

WebSocket events for both mutations are published server-side but the webapp registers no handlers
yet, so a second client sees the change on its next fetch. That gap is not addressed here.
