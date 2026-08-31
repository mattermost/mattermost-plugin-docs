// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Permissions} from 'types/permissions';

import {samePermissionSet, summarizePermissions} from './space_permission_sets';

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
});
