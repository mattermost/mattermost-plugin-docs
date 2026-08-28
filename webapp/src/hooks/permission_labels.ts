// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useIntl} from 'react-intl';
import type {MemberPermissionTier} from 'utils/space_permission_sets';

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

export type PermissionTierLabel = {
    label: string;
    description: string;
};

/**
 * Resolve the named-tier vocabulary under the active locale. One spelling for every surface: the
 * Share footer, the member row menu and the settings tab all name a tier the same way.
 */
export const usePermissionTierLabels = (): Record<MemberPermissionTier | 'custom', PermissionTierLabel> => {
    const {formatMessage} = useIntl();

    return {
        view: {
            label: formatMessage({id: 'docs.permissionTier.view', defaultMessage: 'Can view'}),
            description: formatMessage({id: 'docs.permissionTier.viewDescription', defaultMessage: 'View pages'}),
        },
        comment: {
            label: formatMessage({id: 'docs.permissionTier.comment', defaultMessage: 'Can comment'}),
            description: formatMessage({id: 'docs.permissionTier.commentDescription', defaultMessage: 'View and comment on pages'}),
        },
        edit: {
            label: formatMessage({id: 'docs.permissionTier.edit', defaultMessage: 'Can edit'}),
            description: formatMessage({id: 'docs.permissionTier.editDescription', defaultMessage: 'Create, comment on, edit, and delete their own pages'}),
        },
        admin: {
            label: formatMessage({id: 'docs.permissionTier.admin', defaultMessage: 'Admin'}),
            description: formatMessage({id: 'docs.permissionTier.adminDescription', defaultMessage: 'Manage the space, its members, and every page'}),
        },
        custom: {
            label: formatMessage({id: 'docs.permissionTier.custom', defaultMessage: 'Custom'}),
            description: formatMessage({id: 'docs.permissionTier.customDescription', defaultMessage: 'A combination that matches no tier'}),
        },
    };
};
