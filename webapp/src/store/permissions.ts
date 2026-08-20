// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {GlobalState} from '@mattermost/types/store';

import {Permissions} from 'types/permissions';
import type {Permission} from 'types/permissions';

import {getSpace} from './selectors';

// Whether to offer member management in a space, as the server resolved it.
//
// Read from the server's answer rather than inferred from membership: the roster routes admit a
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

// The caller's own effective permissions in a space, as the server resolved them, or
// undefined when the space has only been seen in a team listing (which carries none).
export const getSpacePermissions = (state: GlobalState, spaceId: string): Permission[] | undefined =>
    getSpace(state, spaceId)?.permissions;

// Whether to offer page creation in a space.
//
// Undefined permissions mean "not resolved yet", not "holds nothing", so the action is
// offered: hiding it then would withhold it from a legitimate author on nothing more than a
// listing that never carried the field. The server refuses either way — this gates the
// affordance, not the authority.
export const getCanCreatePage = (state: GlobalState, spaceId: string): boolean => {
    const permissions = getSpacePermissions(state, spaceId);
    return permissions === undefined || permissions.includes(Permissions.CREATE_PAGE);
};
