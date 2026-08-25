// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {GlobalState} from '@mattermost/types/store';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {Permissions} from 'types/permissions';
import type {Permission} from 'types/permissions';

import {getSpace} from './selectors';

// Whether to offer member management in a space, as the server resolved it.
//
// Read from the server's answer rather than inferred from membership: the roster mutations admit a
// sysadmin, a space admin, or a team manage_space holder, and none of those is "appears in the
// member list". Inferring it from membership was wrong in both directions — it offered the roster
// to every ordinary member, whom the server refuses, and withheld it from a team admin and a
// sysadmin, whom the server serves and who need not appear in the list at all.
//
// Undefined means "not resolved yet" (a space seen only in a team listing carries no answer), and
// is treated as not-permitted here: unlike page creation, this gates an administrative surface, so
// showing it on an unresolved space would advertise authority the caller may not have.
export const getCanManageSpaceMembers = (state: GlobalState, spaceId: string): boolean =>
    getSpacePermissions(state, spaceId)?.includes(Permissions.MANAGE_SPACE) === true;

// Whether to offer archiving and restoring a space, as the server resolved it.
//
// Kept apart from getCanManageSpaceMembers because the routes are: the delete gate admits a
// sysadmin, a space admin, or a team delete_space holder, and that last one is a different team
// permission from the manage tier's. Reading the manage tier here was wrong in both directions —
// it offered archive to a team manage_space holder the route refuses, and hid it from a team
// delete_space holder the route admits.
//
// Undefined means "not resolved yet" and is treated as not-permitted, as the manage tier is:
// archiving is destructive, so offering it on a space whose answer never arrived is the wrong
// direction to guess in.
export const getCanDeleteSpace = (state: GlobalState, spaceId: string): boolean =>
    getSpacePermissions(state, spaceId)?.includes(Permissions.DELETE_SPACE) === true;

// Whether to offer the space's own exposure controls — the default permission set and view
// access. A strictly narrower tier than getCanManageSpaceMembers: those routes also admit a team
// manage_space holder, whom the exposure knobs refuse.
//
// Undefined means "not resolved yet" and is treated as not-permitted, as the manage tier is.
export const getCanAdministerSpace = (state: GlobalState, spaceId: string): boolean =>
    getSpacePermissions(state, spaceId)?.includes(Permissions.ADMIN_SPACE) === true;

// The caller's own effective permissions in a space, as the server resolved them, or
// undefined when the space has only been seen in a team listing (which carries none).
export const getSpacePermissions = (state: GlobalState, spaceId: string): Permission[] | undefined =>
    getSpace(state, spaceId)?.permissions;

// Whether an authoring action is offered in a space: the caller either already holds the
// permission, or is one join away from holding it.
//
// The second arm is what makes an open space usable by someone who has never been added to it. The
// server reports their present-tense permissions truthfully — read_page alone — so an affordance
// keyed on those alone would hide every authoring control from exactly the people an open space
// exists to admit, and the join they need would never be reached. can_join is the server's own
// answer to "may this caller join", so the client never infers it: a guest carries the same
// read_page against the same defaults and must not be offered anything.
//
// Undefined permissions mean "not resolved yet", not "holds nothing", so the action is
// offered: hiding it then would withhold it from a legitimate author on nothing more than a
// listing that never carried the field. The server refuses either way — this gates the
// affordance, not the authority.
const offersAuthoring = (state: GlobalState, spaceId: string, permission: Permission): boolean => {
    const space = getSpace(state, spaceId);
    const permissions = space?.permissions;

    if (permissions === undefined) {
        return true;
    }
    if (permissions.includes(permission)) {
        return true;
    }
    return space?.can_join === true && (space.default_permissions ?? []).includes(permission);
};

// Whether to offer page creation in a space.
export const getCanCreatePage = (state: GlobalState, spaceId: string): boolean =>
    offersAuthoring(state, spaceId, Permissions.CREATE_PAGE);

// Whether to offer editing an already-published page in a space.
//
// Separate from getCanCreatePage because the server separates them: publishing a page that does not
// exist yet is gated on create_page, and publishing over a live one on edit_page (see
// PublishPageDraft). A space default can carry either without the other.
export const getCanEditPage = (state: GlobalState, spaceId: string): boolean =>
    offersAuthoring(state, spaceId, Permissions.EDIT_PAGE);

// Whether to offer deleting a page.
//
// The only page action whose answer depends on which page it is: delete_page covers any page in the
// space, while delete_own_page covers only the ones the caller wrote, so the same member is offered
// it on their own page and refused on someone else's (see ResolveSpaceRemovePage, which tries the
// two in that order).
//
// No join arm, unlike the authoring affordances. Those are offered to a non-member of an open space
// because the seams behind them join first; nothing joins on the way to a delete, so offering it
// would produce exactly the refusal this gate exists to prevent. A member who joined that way is
// unaffected — they hold the space default like any other member.
//
// Unresolved permissions are treated as everywhere else: offered, with the server remaining the
// authority.
export const getCanDeletePage = (state: GlobalState, spaceId: string, pageAuthorId: string): boolean => {
    const permissions = getSpacePermissions(state, spaceId);

    if (permissions === undefined) {
        return true;
    }
    if (permissions.includes(Permissions.DELETE_PAGE)) {
        return true;
    }
    return permissions.includes(Permissions.DELETE_OWN_PAGE) && pageAuthorId === getCurrentUserId(state);
};

// Whether the caller still has to join the space before a write of theirs can land, as the server
// resolved it. The authoring affordances above are offered on the strength of this, so the join has
// to happen before the write they lead to is sent.
export const getMustJoinSpace = (state: GlobalState, spaceId: string): boolean =>
    getSpace(state, spaceId)?.can_join === true;
