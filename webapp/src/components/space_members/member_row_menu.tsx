// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {usePermissionLabels} from 'hooks/permission_labels';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {summarizePermissions} from 'utils/space_permission_sets';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import DotsVerticalIcon from '@mattermost/compass-icons/components/dots-vertical';

import {Button} from 'components/form_controls/button';
import Menu from 'components/menu/menu';

import type {Permission} from 'types/permissions';

import styles from './space_members.module.scss';

export type MemberPermissionMenu = {
    options: readonly Permission[];
    selected: readonly Permission[];
    effective: readonly Permission[];
    disabled: boolean;
    disabledOptions?: readonly Permission[];
    onChange: (next: Permission[]) => void;
};

type Props = {
    member: MemberProfile;
    role?: 'admin' | 'member' | 'guest';
    permissionMenu?: MemberPermissionMenu;
    isCurrentUser: boolean;

    /** A mutation is in flight; the action is unavailable but the menu still opens. */
    disabled: boolean;
    onRemove: () => void;
    onLeave: () => void;
};

/**
 * The role/membership menu on a member row.
 *
 * A resolved permission record turns the existing role dropdown into the granular capability
 * editor. A profile-only roster still gets an icon-only membership actions menu.
 */
const MemberRowMenu = ({member, role, permissionMenu, isCurrentUser, disabled, onRemove, onLeave}: Props) => {
    const {formatMessage} = useIntl();
    const permissionLabels = usePermissionLabels();

    // The trigger always describes the action independently of the visible role or permission summary.
    const triggerLabel = permissionMenu ? formatMessage(
        {id: 'docs.spaceMembers.menu.permissionsLabel', defaultMessage: 'Permissions for {name}'},
        {name: member.displayName},
    ) : formatMessage(
        {id: 'docs.spaceMembers.menu.moreActionsLabel', defaultMessage: 'More actions for {name}'},
        {name: member.displayName},
    );
    let roleLabel;
    if (role === 'admin') {
        roleLabel = (
            <FormattedMessage
                id='docs.spaceMembers.role.admin'
                defaultMessage='Admin'
            />
        );
    } else if (role === 'guest') {
        roleLabel = (
            <FormattedMessage
                id='docs.spaceMembers.role.guest'
                defaultMessage='Guest'
            />
        );
    } else if (role === 'member') {
        const summary = permissionMenu && summarizePermissions(permissionMenu.effective);
        if (summary === 'view') {
            roleLabel = (
                <FormattedMessage
                    id='docs.spaceMembers.role.canView'
                    defaultMessage='Can view'
                />
            );
        } else if (summary === 'comment') {
            roleLabel = (
                <FormattedMessage
                    id='docs.spaceMembers.role.canComment'
                    defaultMessage='Can comment'
                />
            );
        } else if (summary === 'edit') {
            roleLabel = (
                <FormattedMessage
                    id='docs.spaceMembers.role.canEdit'
                    defaultMessage='Can edit'
                />
            );
        } else if (summary === 'custom') {
            roleLabel = (
                <FormattedMessage
                    id='docs.spaceMembers.role.custom'
                    defaultMessage='Custom'
                />
            );
        } else {
            roleLabel = (
                <FormattedMessage
                    id='docs.spaceMembers.role.member'
                    defaultMessage='Member'
                />
            );
        }
    } else {
        roleLabel = <DotsVerticalIcon size={16}/>;
    }

    const trigger = (
        <Button
            type='button'
            emphasis='quaternary'
            size='sm'
            className={styles.roleTrigger}
            aria-label={triggerLabel}
        >
            {roleLabel}
            {role && <ChevronDownIcon size={16}/>}
        </Button>
    );

    return (
        <Menu
            ariaLabel={formatMessage(
                {id: 'docs.spaceMembers.menu.ariaLabel', defaultMessage: 'Membership options for {name}'},
                {name: member.displayName},
            )}
            align='right'
            trigger={trigger}
        >
            {permissionMenu && (
                <>
                    {permissionMenu.options.map((permission) => (
                        <Menu.CheckboxItem
                            key={permission}
                            checked={permissionMenu.selected.includes(permission)}
                            disabled={permissionMenu.disabled || permissionMenu.disabledOptions?.includes(permission)}
                            onCheckedChange={(checked) => {
                                if (permissionMenu.disabled || permissionMenu.disabledOptions?.includes(permission)) {
                                    return;
                                }

                                permissionMenu.onChange(permissionMenu.options.filter((option) => (
                                    option === permission ? checked : permissionMenu.selected.includes(option)
                                )));
                            }}
                        >
                            {permissionLabels[permission]}
                        </Menu.CheckboxItem>
                    ))}

                    <Menu.Separator/>
                </>
            )}

            {isCurrentUser ? (
                <Menu.Item
                    destructive={true}
                    disabled={disabled}
                    onClick={onLeave}
                >
                    <FormattedMessage
                        id='docs.spaceMembers.leave'
                        defaultMessage='Leave space'
                    />
                </Menu.Item>
            ) : (
                <Menu.Item
                    destructive={true}
                    disabled={disabled}
                    onClick={onRemove}
                >
                    <FormattedMessage
                        id='docs.spaceMembers.remove'
                        defaultMessage='Remove from space'
                    />
                </Menu.Item>
            )}
        </Menu>
    );
};

export default MemberRowMenu;
