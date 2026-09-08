// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {usePermissionLabels} from 'hooks/permission_labels';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import DotsVerticalIcon from '@mattermost/compass-icons/components/dots-vertical';

import {Button} from 'components/form_controls/button';
import Menu from 'components/menu/menu';

import type {Permission} from 'types/permissions';

import styles from './space_members.module.scss';

export type MemberPermissionMenu = {

    // What this caller may grant this member. A permission the server would refuse from this
    // caller is absent rather than present and disabled, so the menu offers only writes that
    // can succeed; a caller with no grantable permission gets no menu at all.
    options: readonly Permission[];
    selected: readonly Permission[];

    /** A write is in flight or the record is still loading; the menu opens but cannot be used. */
    disabled: boolean;

    /** Shown as a checkbox's secondaryLabel while `disabled`. */
    disabledReason?: string;
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
 * A resolved permission record turns the role dropdown into a grant editor over the individual
 * permission ids. It offers no named tier: a tier names a seeded space-default scheme, and a
 * member's grant selects no scheme — it adds atomic roles on top of whatever the space default
 * already gives them. A profile-only roster still gets an icon-only membership actions menu.
 */
const MemberRowMenu = ({member, role, permissionMenu, isCurrentUser, disabled, onRemove, onLeave}: Props) => {
    const {formatMessage} = useIntl();
    const permissionLabels = usePermissionLabels();

    // roleText is the plain-string form of the visible standing, folded into the trigger's
    // accessible name below (WCAG 2.5.3): a screen reader or voice-control user must hear/match
    // the same text a sighted user sees on the button. It names the member's standing —
    // administrator, guest, ordinary member — not the size of their grant.
    let roleText: string | undefined;
    if (role === 'admin') {
        roleText = formatMessage({id: 'docs.spaceMembers.role.admin', defaultMessage: 'Admin'});
    } else if (role === 'guest') {
        roleText = formatMessage({id: 'docs.spaceMembers.role.guest', defaultMessage: 'Guest'});
    } else if (role === 'member') {
        roleText = formatMessage({id: 'docs.spaceMembers.role.member', defaultMessage: 'Member'});
    }
    const roleLabel = roleText ?? <DotsVerticalIcon size={16}/>;

    let triggerLabel;
    if (!roleText) {
        triggerLabel = formatMessage(
            {id: 'docs.spaceMembers.menu.moreActionsLabel', defaultMessage: 'More actions for {name}'},
            {name: member.displayName},
        );
    } else if (permissionMenu) {
        triggerLabel = formatMessage(
            {id: 'docs.spaceMembers.menu.permissionsLabel', defaultMessage: '{role} — permissions for {name}'},
            {role: roleText, name: member.displayName},
        );
    } else {
        triggerLabel = formatMessage(
            {id: 'docs.spaceMembers.menu.roleActionsLabel', defaultMessage: '{role} — more actions for {name}'},
            {role: roleText, name: member.displayName},
        );
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
                            disabled={permissionMenu.disabled}
                            secondaryLabel={permissionMenu.disabled ? permissionMenu.disabledReason : undefined}
                            onCheckedChange={(checked) => {
                                if (permissionMenu.disabled) {
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
