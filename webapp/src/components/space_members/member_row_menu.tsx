// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import DotsVerticalIcon from '@mattermost/compass-icons/components/dots-vertical';

import {Button} from 'components/form_controls/button';
import Menu from 'components/menu/menu';

import styles from './space_members.module.scss';

type Props = {
    member: MemberProfile;
    role?: 'admin' | 'member' | 'guest';
    isCurrentUser: boolean;

    /** A mutation is in flight; the action is unavailable but the menu still opens. */
    disabled: boolean;
    onRemove: () => void;
    onLeave: () => void;
};

/**
 * The role/membership menu on a member row.
 *
 * When the caller resolved a role, its label and the disabled role scaffolding are shown. A
 * profile-only roster has no role to report, so it gets an icon-only action menu instead. Per-member
 * permissions are edited in the space settings modal's Permissions tab, not here.
 */
const MemberRowMenu = ({member, role, isCurrentUser, disabled, onRemove, onLeave}: Props) => {
    const {formatMessage} = useIntl();

    // The trigger always describes the action, independently of the member's role. Admin, Member,
    // and Guest are labels, not separate management personas; folding "manage" into the accessible
    // name made an ordinary member sound like a manager and forced callers to know the current role
    // merely to open the same actions menu.
    const triggerLabel = formatMessage(
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
        roleLabel = (
            <FormattedMessage
                id='docs.spaceMembers.role.member'
                defaultMessage='Member'
            />
        );
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
            {role && (
                <>
                    <Menu.Item disabled={true}>
                        <FormattedMessage
                            id='docs.spaceMembers.role.admin'
                            defaultMessage='Admin'
                        />
                    </Menu.Item>
                    <Menu.Item disabled={true}>
                        <FormattedMessage
                            id='docs.spaceMembers.role.canEdit'
                            defaultMessage='Can edit'
                        />
                    </Menu.Item>
                    <Menu.Item disabled={true}>
                        <FormattedMessage
                            id='docs.spaceMembers.role.canView'
                            defaultMessage='Can view'
                        />
                    </Menu.Item>

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
