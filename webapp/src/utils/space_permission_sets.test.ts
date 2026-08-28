// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Permissions} from 'types/permissions';

import {samePermissionSet, summarizeMemberPermissions, summarizePermissions} from './space_permission_sets';

describe('space permission sets', () => {
    it('compares permission sets independently of order and duplicates', () => {
        expect(samePermissionSet(
            [Permissions.COMMENT_PAGE, Permissions.CREATE_PAGE, Permissions.COMMENT_PAGE],
            [Permissions.CREATE_PAGE, Permissions.COMMENT_PAGE],
        )).toBe(true);
        expect(samePermissionSet([Permissions.COMMENT_PAGE], [Permissions.EDIT_PAGE])).toBe(false);
    });

    it.each([
        [[Permissions.READ_PAGE], 'view'],
        [[Permissions.READ_PAGE, Permissions.COMMENT_PAGE], 'comment'],
        [[
            Permissions.READ_PAGE,
            Permissions.CREATE_PAGE,
            Permissions.COMMENT_PAGE,
            Permissions.EDIT_PAGE,
            Permissions.DELETE_OWN_PAGE,
        ], 'edit'],
        [[Permissions.READ_PAGE, Permissions.CREATE_PAGE], 'custom'],
    ] as const)('summarizes %j as %s', (permissions, summary) => {
        expect(summarizePermissions(permissions)).toBe(summary);
    });

    it('does not let space administration capabilities change the content summary', () => {
        expect(summarizePermissions([
            Permissions.READ_PAGE,
            Permissions.ADMIN_SPACE,
            Permissions.MANAGE_SPACE,
            Permissions.DELETE_SPACE,
        ])).toBe('view');
    });

    // A member's tier is what they can do, which the space default sets a floor for; only the
    // admin_space grant names a tier of its own.
    it('summarizes a member by their effective set unless they hold admin_space', () => {
        expect(summarizeMemberPermissions([], [Permissions.READ_PAGE, Permissions.COMMENT_PAGE])).toBe('comment');
        expect(summarizeMemberPermissions([Permissions.EDIT_PAGE], [Permissions.READ_PAGE, Permissions.EDIT_PAGE])).toBe('custom');
        expect(summarizeMemberPermissions([Permissions.ADMIN_SPACE], [Permissions.READ_PAGE])).toBe('admin');
    });
});
