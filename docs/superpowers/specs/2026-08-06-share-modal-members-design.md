# Space members: add and remove, shared across surfaces

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

The same roster has been written three times, and none of the copies work:

- `share_space_modal.tsx` — rows with an inert `Admin ▾`, a real `PeoplePicker` wired to nothing.
- `space_settings_modal.tsx`'s `PermissionsTab` — its own row markup, its own `@{username}` message
  id, its own inert role trigger, and a search field that is a `div` with `aria-disabled` rather
  than a combobox at all.
- `space_info_members.tsx` — read-only rows, a third set of message ids and styles.

So this is two jobs, and the second is why the first is worth doing carefully: make membership work,
and make it work *once*. The membership internals become a single core that each surface wraps with
its own chrome and its own affordances.

This spec covers only membership: adding people, removing people, and leaving. Roles and space
view-access remain scaffolding for PR #10 (MM-69269).

## Scope

In scope:

- `addSpaceMember` on the data source, over the existing POST route.
- Store action types and reducer cases for adding and removing one member.
- A singular thunk for each mutation, plus a separate bulk add thunk composed from the singular one.
- A hook that owns the toasts and the busy state.
- A shared `components/space_members/` core: the roster, the row, the row menu, and the add field.
- Three wrappers over that core — Share modal, Space Settings → Permissions, space info panel —
  each with its own chrome, and only the Share modal and Permissions tab getting write affordances.
- Extracting `PermissionsTab` out of the 592-line `space_settings_modal.tsx` into its own file.
- Correcting the two stale comments.

Out of scope:

- Per-member roles and the role dropdown's behaviour (PR #10 capabilities).
- Space view-access — the `Public` / `Can View` footer controls (PR #10 `view_access`).
- Flipping `canManageMembers`. It is already `true` via the dev override, and that override's
  comment stays accurate as the thing to revisit when real capabilities arrive.
- `PeoplePicker`'s internals. Its multi-select chip UI is exactly what this flow needs; it moves
  into the shared core unchanged apart from its import path.
- Bulk remove. The bulk add thunk establishes the composition pattern; a bulk remove can follow the
  same shape if a surface ever needs it.
- The Permissions tab's other sections — the public/private selector and the external-sharing
  toggle. Both stay scaffolding for PR #10; only the people section is rebuilt on the core.
- Anything in the settings modal's dirty/save flow. See "Immediacy" below.

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
- Toasts are raised here, not in the components, so both writing surfaces get one error vocabulary
  without either of them restating it.

The hook is the only layer that knows about both profiles and messages. The thunks deal in ids and
errors; the core components deal in chips and rows.

Each writing wrapper calls the hook once and threads its returned functions into the core as
`onAdd` / `actions`. The core never calls the hook itself — that keeps it a presentation layer with
no opinion on where its data comes from, which is what lets the read-only panel use the same roster
with no hook at all.

### The shared core — `components/space_members/`

One place that knows what a space member looks like and what you can do to one. Each part answers a
single question, and none of them know which surface is rendering them.

```
components/space_members/
  member_list.tsx        roster: maps profiles to rows, owns the empty state
  member_row.tsx         one member: avatar, name, @handle, (You), trailing slot
  member_row_menu.tsx    the Admin ▾ menu: disabled roles + Remove / Leave
  add_members_field.tsx  PeoplePicker + Add button + pending state
  people_picker.tsx      moved from share_space_modal/, otherwise unchanged
  space_members.module.scss
  index.ts
```

`MemberList` takes the data and a description of the row's affordances, not a surface name:

```ts
type MemberListProps = {
    members: MemberProfile[];
    avatarSize: 'sm' | 'md';
    showYouBadge?: boolean;

    // Absent = read-only roster (the space info panel). Present = each row gets
    // the role/remove menu, driven by these.
    actions?: {
        onRemove: (userId: string) => void;
        onLeave: () => void;
        disabled: boolean;
    };
};
```

Passing `actions` as an optional bag rather than a `readOnly` boolean is deliberate: a read-only
roster is one that was given no actions, so there is no way to render a menu with nothing behind it,
and no surface flag for the row to branch on.

`AddMembersField` owns the whole add interaction — the picker, the chips, the `pending` state, the
`Add` button, and putting failures back into `pending`:

```ts
type AddMembersFieldProps = {
    excludeIds: string[];
    onAdd: (users: MemberProfile[]) => Promise<MemberProfile[]>;  // resolves to failures
    disabled: boolean;
};
```

`onAdd` is `useManageSpaceMembers().addMembers` at both call sites. The field is what unions
`excludeIds` with its own pending selections, so a consumer passes only the member ids and never has
to know that pending selections exist.

`MemberRowMenu` wraps the row's `Admin ▾` trigger in `components/menu`:

- Role items (`Admin`, `Can edit`, `Can view`) render with `disabled`, carrying a comment that they
  light up with PR #10's capabilities. They are shown rather than hidden so the menu does not
  visibly change shape when that lands.
- `Menu.Separator`, then the action: `Remove from space` with `destructive`, or `Leave space` on the
  current user's own row.

#### i18n consolidation

The core owns one set of message ids under `docs.spaceMembers.*`. The three per-surface duplicates
of the same strings — `docs.share.handle`, `docs.spaceSettings.permissions.handle`,
`docs.spaceInfo.handle`, and likewise for the role labels — are deleted. This is a deliberate
translation-key change: the English defaults are identical, so nothing reads differently, but any
existing translations for the old ids will need re-keying.

### Wrappers

What differs per surface is only chrome and which affordances are handed in:

| | Share modal | Settings → Permissions | Space info panel |
| --- | --- | --- | --- |
| Add field | yes | yes, replacing the fake search `div` | no |
| Row menu | yes | yes | no — read-only |
| Avatar size | `md` | `sm` | `sm` |
| `(You)` badge | yes | no | no |
| Surrounding chrome | modal body | `<Section>` inside the tab | panel scroll area |
| Leave closes | the modal | the modal | n/a |

`components/share_space_modal/share_space_modal.tsx` becomes an `AddMembersField` plus a
`MemberList` with actions. Its inline row markup, role trigger and chip wiring all move to the core.
The two stale comments about there being no add-member API are removed; the remaining comment
narrows to what is still true, that roles and view-access are PR #10 scaffolding.

`components/space_settings_modal/permissions_tab.tsx`, extracted from the 592-line modal file.
Same two components, wrapped in the existing `Section`. Its hand-rolled member rows and its
`aria-disabled` pretend-search are deleted, which is the point of the port: the tab gains a working
add and remove by using the core rather than by growing a second implementation of it. The
public/private selector and external-sharing toggle above and below it are untouched.

`components/space_info/space_info_members.tsx` becomes a `MemberList` with no `actions` — the
read-only case that proves the core does not assume write affordances. This is the third duplicate;
leaving it behind would mean shipping the consolidation and the thing it was meant to remove.

#### Immediacy

Membership changes apply immediately and must **not** join the settings modal's dirty/save flow. That
footer is driven by `useInfoTab`, whose fields are edits to the space row that the user saves or
discards as a set. A member add is a completed server action the moment it returns; routing it
through `SaveChangesBar` would imply it could be discarded, which it cannot. The Permissions tab
therefore never marks the modal dirty.

### Sequencing

The core is built with its first consumer, not before it and not after both:

1. Data source, action types, reducer, thunks, hook — no UI.
2. The core components, together with the Share modal wrapper. Building the core against one real
   consumer keeps its props honest; building it speculatively invites a shape nothing needs.
3. Port the Permissions tab. This is where the props get their second opinion — anything that turns
   out to be Share-specific surfaces here and moves out of the core.
4. Port the space info panel, exercising the no-`actions` path.

Steps 3 and 4 are ports, not rewrites: if they need core changes beyond passing different props,
that is a signal step 2 guessed wrong, and the fix belongs in the core.

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
- `components/space_members/member_list.test.tsx` — with `actions` each row has a menu; without
  `actions` no menu renders anywhere; the `(You)` badge follows `showYouBadge`.
- `components/space_members/add_members_field.test.tsx` — Add is disabled with no chips and while
  disabled; clicking it calls `onAdd` with the pending users; failed users remain as chips and
  successful ones do not; pending selections are excluded from suggestions alongside `excludeIds`.
- `components/space_members/member_row_menu.test.tsx` — role items are disabled; another user's row
  offers Remove, the current user's row offers Leave.
- `components/share_space_modal/share_space_modal.test.tsx` — trimmed to the wrapper's own job: it
  renders the add field and a roster with actions, and Leave closes the modal. The row and chip
  behaviour is covered by the core's tests rather than re-asserted here.
- `components/space_settings_modal/permissions_tab.test.tsx` — renders the add field and an
  actionable roster; adding or removing a member does not mark the modal dirty (no `SaveChangesBar`).
- The existing `space_settings_modal.test.tsx` keeps passing unchanged, which is the check that
  extracting the tab into its own file changed nothing observable.

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

Space Settings → Permissions gains working add and remove as a side effect of the port. That is
intended — it is the same capability the Share modal gets, reached from a different place — but it
does mean the Permissions tab stops being purely scaffolding, so its "Coming soon" affordances now
sit next to controls that really work. The public/private selector and external-sharing toggle keep
their disabled state and their coming-soon labels, which is what distinguishes them.

Three surfaces now share one roster, so a change to the row — a role dropdown coming alive in
PR #10, an avatar tweak, a new badge — lands everywhere at once. That is the payoff, and also the
new risk: the core has no per-surface escape hatch by design, so a genuinely surface-specific need
has to either become a prop or stay out of the core.
