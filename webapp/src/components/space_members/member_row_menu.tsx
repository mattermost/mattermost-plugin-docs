// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {usePermissionLabels, usePermissionTierLabels} from 'hooks/permission_labels';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {MEMBER_PERMISSION_TIERS, TIER_PERMISSIONS, summarizeMemberPermissions, type MemberPermissionTier} from 'utils/space_permission_sets';

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

    /** Shown as a checkbox's secondaryLabel when it is disabled via `disabled`. */
    disabledReason?: string;

    /** Shown as a checkbox's secondaryLabel when it is disabled via `disabledOptions`. */
    disabledOptionsReason?: string;
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
 * A resolved permission record turns the role dropdown into a permission editor: the named
 * tiers first, then the individual permissions that refine them. A profile-only roster still
 * gets an icon-only membership actions menu.
 */
const MemberRowMenu = ({member, role, permissionMenu, isCurrentUser, disabled, onRemove, onLeave}: Props) => {
    const {formatMessage} = useIntl();
    const permissionLabels = usePermissionLabels();
    const tierLabels = usePermissionTierLabels();

    const memberTier = permissionMenu ? summarizeMemberPermissions(permissionMenu.selected, permissionMenu.effective) : undefined;

    // roleText is the plain-string form of the visible role/tier label, folded into the
    // trigger's accessible name below (WCAG 2.5.3): a screen reader or voice-control user must
    // hear/match the same text a sighted user sees on the button.
    let roleText: string | undefined;
    if (role === 'admin') {
        roleText = tierLabels.admin.label;
    } else if (role === 'guest') {
        roleText = formatMessage({id: 'docs.spaceMembers.role.guest', defaultMessage: 'Guest'});
    } else if (role === 'member') {
        roleText = memberTier ? tierLabels[memberTier].label : formatMessage({id: 'docs.spaceMembers.role.member', defaultMessage: 'Member'});
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

    // A tier is offered only when every permission it stands for is one this menu may grant.
    const tiers = permissionMenu ? MEMBER_PERMISSION_TIERS.filter((tier) => (
        TIER_PERMISSIONS[tier].every((permission) => permissionMenu.options.includes(permission))
    )) : [];

    const permissionDisabled = (permission: Permission) => (
        Boolean(permissionMenu?.disabled) || Boolean(permissionMenu?.disabledOptions?.includes(permission))
    );

    const permissionDisabledReason = (permission: Permission): string | undefined => {
        if (permissionMenu?.disabled) {
            return permissionMenu.disabledReason;
        }
        if (permissionMenu?.disabledOptions?.includes(permission)) {
            return permissionMenu.disabledOptionsReason;
        }
        return undefined;
    };

    const tierDisabled = (tier: MemberPermissionTier) => (
        TIER_PERMISSIONS[tier].some(permissionDisabled) || Boolean(permissionMenu?.disabled)
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
                    {tiers.length > 0 && (
                        <Menu.RadioGroup
                            value={memberTier}
                            onValueChange={(tier) => {
                                const nextTier = tier as MemberPermissionTier;
                                if (tierDisabled(nextTier)) {
                                    return;
                                }

                                // Sent in the menu's own option order, like the checkboxes below.
                                permissionMenu.onChange(permissionMenu.options.filter((option) => TIER_PERMISSIONS[nextTier].includes(option)));
                            }}
                        >
                            {tiers.map((tier) => (
                                <Menu.RadioItem
                                    key={tier}
                                    value={tier}
                                    secondaryLabel={tierLabels[tier].description}
                                    disabled={tierDisabled(tier)}
                                >
                                    {tierLabels[tier].label}
                                </Menu.RadioItem>
                            ))}
                        </Menu.RadioGroup>
                    )}
                    {tiers.length > 0 && <Menu.Separator/>}
                    {permissionMenu.options.map((permission) => (
                        <Menu.CheckboxItem
                            key={permission}
                            checked={permissionMenu.selected.includes(permission)}
                            disabled={permissionDisabled(permission)}
                            secondaryLabel={permissionDisabledReason(permission)}
                            onCheckedChange={(checked) => {
                                if (permissionDisabled(permission)) {
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
