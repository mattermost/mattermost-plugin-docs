// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {apiUrl, restGet, restPatch, restPut} from 'client/rest';

import type {UserProfile} from '@mattermost/types/users';

import {Client4} from 'mattermost-redux/client';

import type {Capability, Paginated, SpaceAccess, SpaceMember, SpaceViewAccess} from 'types/permissions';

// The space-permissions calls: the space's default capability set and each
// member's granted set. Kept apart from the Docs data source (spaces, pages)
// because those still read from the mock source until the Spaces UI branch
// lands its API-backed one; a permission control that wrote to a fixture would
// be worse than no control.

// Ids are server-generated and URL-safe, so this is defence in depth: an id that
// ever arrives malformed cannot reshape the request path.
const seg = encodeURIComponent;

// getSpaceAccess reads a single space. Only the access fields are typed here;
// the response also carries the plain Space fields.
export const getSpaceAccess = (spaceId: string): Promise<SpaceAccess> =>
    restGet<SpaceAccess>(`${apiUrl()}/spaces/${seg(spaceId)}`);

// getSpaceMembers reads one page of a space's members. Requires manage authority
// over the space; a caller without it gets a 403.
export const getSpaceMembers = (spaceId: string, page: number, perPage: number): Promise<Paginated<SpaceMember>> =>
    restGet<Paginated<SpaceMember>>(`${apiUrl()}/spaces/${seg(spaceId)}/members?page=${page}&per_page=${perPage}`);

// setMemberCapabilities replaces a member's granted set. The empty array is the
// deliberate clear; the field is always sent, since omitting it is a 400.
export const setMemberCapabilities = (spaceId: string, userId: string, granted: Capability[]): Promise<SpaceMember> =>
    restPut<SpaceMember>(
        `${apiUrl()}/spaces/${seg(spaceId)}/members/${seg(userId)}/capabilities`,
        {granted_capabilities: granted},
    );

// setDefaultCapabilities replaces the space's default capability set — what every
// member holds without a per-member grant. Requires space admin.
export const setDefaultCapabilities = (spaceId: string, capabilities: Capability[]): Promise<SpaceAccess> =>
    restPut<SpaceAccess>(
        `${apiUrl()}/spaces/${seg(spaceId)}/default-capabilities`,
        {default_capabilities: capabilities},
    );

// setSpaceViewAccess flips the space between open and private. Requires space
// admin. expectedUpdateAt is sent so a concurrent edit surfaces as the
// server's 409 conflict message rather than silently overwriting it.
export const setSpaceViewAccess = (spaceId: string, viewAccess: SpaceViewAccess, expectedUpdateAt: number): Promise<SpaceAccess> =>
    restPatch<SpaceAccess>(
        `${apiUrl()}/spaces/${seg(spaceId)}`,
        {view_access: viewAccess, expected_update_at: expectedUpdateAt},
    );

// getMemberProfiles resolves member ids to user profiles for display. A platform
// read rather than a Docs one, so it goes through Client4's own surface.
export const getMemberProfiles = (userIds: string[]): Promise<UserProfile[]> => {
    if (userIds.length === 0) {
        return Promise.resolve([]);
    }
    return Client4.getProfilesByIds(userIds);
};
