// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {apiUrl, listAll, restGet, restPatch, restPut} from 'client/rest';

import type {Permission, SpaceAccess, SpaceMember, SpaceViewAccess} from 'types/permissions';

// The space-permissions calls: the space's default permission set and each
// member's granted set. Kept apart from the Docs data source (spaces, pages),
// which still reads from a mock fixture; a permission control that wrote to a
// fixture would be worse than no control.

// Ids are server-generated and URL-safe, so this is defence in depth: an id that
// ever arrives malformed cannot reshape the request path.
const seg = encodeURIComponent;

// getSpaceAccess reads a single space. Only the access fields are typed here;
// the response also carries the plain Space fields.
export const getSpaceAccess = (spaceId: string): Promise<SpaceAccess> =>
    restGet<SpaceAccess>(`${apiUrl()}/spaces/${seg(spaceId)}`);

// listAllSpaceMembers reads every page of a space's members. The whole roster, not a
// page of it: the permissions surface edits a grant per row, so a page boundary would
// hide members whose grants the caller is there to change. The route serves every reader,
// but emits each member's permission set only to a caller holding manage authority over
// the space — so only call this behind that tier.
export const listAllSpaceMembers = (spaceId: string): Promise<SpaceMember[]> =>
    listAll<SpaceMember>((query) => `${apiUrl()}/spaces/${seg(spaceId)}/members?${query}`);

// setMemberPermissions replaces a member's granted set. The empty array is the
// deliberate clear; the field is always sent, since omitting it is a 400.
export const setMemberPermissions = (spaceId: string, userId: string, granted: Permission[]): Promise<SpaceMember> =>
    restPut<SpaceMember>(
        `${apiUrl()}/spaces/${seg(spaceId)}/members/${seg(userId)}/permissions`,
        {granted_permissions: granted},
    );

// setDefaultPermissions replaces the space's default permission set — what every
// member holds without a per-member grant. Requires space admin.
export const setDefaultPermissions = (spaceId: string, permissions: Permission[]): Promise<SpaceAccess> =>
    restPut<SpaceAccess>(
        `${apiUrl()}/spaces/${seg(spaceId)}/default-permissions`,
        {default_permissions: permissions},
    );

// setSpaceViewAccess flips the space between open and private. Requires space
// admin. expectedUpdateAt is sent so a concurrent edit surfaces as the
// server's 409 conflict message rather than silently overwriting it.
export const setSpaceViewAccess = (spaceId: string, viewAccess: SpaceViewAccess, expectedUpdateAt: number): Promise<SpaceAccess> =>
    restPatch<SpaceAccess>(
        `${apiUrl()}/spaces/${seg(spaceId)}`,
        {view_access: viewAccess, expected_update_at: expectedUpdateAt},
    );
