// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';

import {Button} from 'components/form_controls/button';
import Menu from 'components/menu/menu';

import styles from './space_members.module.scss';

type Props = {
    member: MemberProfile;
    isCurrentUser: boolean;

    /** A mutation is in flight; the action is unavailable but the menu still opens. */
    disabled: boolean;
    onRemove: () => void;
    onLeave: () => void;
};

/**
 * The role/membership menu on a member row.
 *
 * Role items are rendered disabled rather than hidden, so the menu does not change
 * shape when PR #10's capabilities make them real.
 */
const MemberRowMenu = ({member, isCurrentUser, disabled, onRemove, onLeave}: Props) => {
    const {formatMessage} = useIntl();

    const trigger = (
        <Button
            type='button'
            emphasis='quaternary'
            size='sm'
            className={styles.roleTrigger}
            aria-label={formatMessage(
                {id: 'docs.spaceMembers.menu.label', defaultMessage: 'Admin, manage {name}'},
                {name: member.displayName},
            )}
        >
            <FormattedMessage
                id='docs.spaceMembers.role.admin'
                defaultMessage='Admin'
            />
            <ChevronDownIcon size={16}/>
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
