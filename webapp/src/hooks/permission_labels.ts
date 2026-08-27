// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useIntl} from 'react-intl';

import type {Permission} from 'types/permissions';
import {Permissions} from 'types/permissions';

/** Resolve the permission vocabulary under the active locale. */
export const usePermissionLabels = (): Record<Permission, string> => {
    const {formatMessage} = useIntl();

    return {
        [Permissions.READ_PAGE]: formatMessage({id: 'docs.permission.read_page', defaultMessage: 'View pages'}),
        [Permissions.CREATE_PAGE]: formatMessage({id: 'docs.permission.create_page', defaultMessage: 'Create pages'}),
        [Permissions.COMMENT_PAGE]: formatMessage({id: 'docs.permission.comment_page', defaultMessage: 'Comment on pages'}),
        [Permissions.EDIT_PAGE]: formatMessage({id: 'docs.permission.edit_page', defaultMessage: 'Edit pages'}),
        [Permissions.DELETE_OWN_PAGE]: formatMessage({id: 'docs.permission.delete_own_page', defaultMessage: 'Delete own pages'}),
        [Permissions.MANAGE_SPACE]: formatMessage({id: 'docs.permission.manage_space', defaultMessage: 'Manage space'}),
        [Permissions.DELETE_SPACE]: formatMessage({id: 'docs.permission.delete_space', defaultMessage: 'Archive space'}),
        [Permissions.DELETE_PAGE]: formatMessage({id: 'docs.permission.delete_page', defaultMessage: 'Delete any page'}),
        [Permissions.ADMIN_SPACE]: formatMessage({id: 'docs.permission.admin_space', defaultMessage: 'Administer space'}),
    };
};
