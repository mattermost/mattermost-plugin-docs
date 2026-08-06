// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useAppSelector} from 'hooks/redux';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import {openDocsModal} from 'components/modals';

import MemberRow from './member_row';
import MemberRowMenu from './member_row_menu';
import styles from './space_members.module.scss';

export type MemberListActions = {
    onRemove: (userId: string) => void;
    onLeave: () => void;

    /** A mutation is in flight; row actions are unavailable. */
    disabled: boolean;
};

type Props = {
    members: MemberProfile[];
    avatarSize: 'sm' | 'md';
    showYouBadge?: boolean;

    /** Names the space in the leave confirmation. Only needed alongside `actions`. */
    spaceTitle?: string;

    // Absent means a read-only roster. Expressed as the absence of actions rather
    // than a flag, so a row can never render a menu with nothing behind it.
    actions?: MemberListActions;
};

/**
 * A space's member roster. Shared by the Share modal, Space Settings → Permissions
 * and the space info panel, which differ only in chrome and affordances.
 *
 * Removing someone and leaving are both irreversible from here — a removed member
 * loses access immediately, and leaving a private space can strand the user outside
 * it — so both confirm first. Confirming here rather than in each surface keeps the
 * copy in one place and means a new surface cannot forget it.
 */
const MemberList = ({members, avatarSize, showYouBadge = false, spaceTitle, actions}: Props) => {
    const currentUserId = useAppSelector(getCurrentUserId);

    const confirmRemove = (member: MemberProfile) => openDocsModal((modal) => (
        <ConfirmModal
            title={(
                <FormattedMessage
                    id='docs.spaceMembers.remove.confirm.title'
                    defaultMessage='Remove {name}'
                    values={{name: member.displayName}}
                />
            )}
            confirmButtonText={(
                <FormattedMessage
                    id='docs.spaceMembers.remove.confirm.button'
                    defaultMessage='Yes, remove'
                />
            )}
            isConfirmDestructive={true}

            // Closing is the confirm's job too: the modal runs `after` INSTEAD of
            // its own onClose, so a handler that doesn't close leaks a stack entry.
            onConfirm={() => {
                modal.close();
                actions?.onRemove(member.id);
            }}
            onCancel={modal.close}
        >
            <FormattedMessage
                id='docs.spaceMembers.remove.confirm.message'
                defaultMessage='Are you sure you want to remove <b>{name}</b> from this space? They will lose access to its pages.'
                values={{
                    name: member.displayName,
                    b: (chunks) => <b>{chunks}</b>,
                }}
            />
        </ConfirmModal>
    ));

    const confirmLeave = () => openDocsModal((modal) => (
        <ConfirmModal
            title={(
                <FormattedMessage
                    id='docs.spaceMembers.leave.confirm.title'
                    defaultMessage='Leave space'
                />
            )}
            confirmButtonText={(
                <FormattedMessage
                    id='docs.spaceMembers.leave.confirm.button'
                    defaultMessage='Yes, leave space'
                />
            )}
            isConfirmDestructive={true}
            onConfirm={() => {
                modal.close();
                actions?.onLeave();
            }}
            onCancel={modal.close}
        >
            {spaceTitle ? (
                <FormattedMessage
                    id='docs.spaceMembers.leave.confirm.message'
                    defaultMessage='Are you sure you want to leave the <b>{name}</b> space? You can rejoin later if it is public.'
                    values={{
                        name: spaceTitle,
                        b: (chunks) => <b>{chunks}</b>,
                    }}
                />
            ) : (
                <FormattedMessage
                    id='docs.spaceMembers.leave.confirm.messageGeneric'
                    defaultMessage='Are you sure you want to leave this space? You can rejoin later if it is public.'
                />
            )}
        </ConfirmModal>
    ));

    return (
        <div className={styles.memberList}>
            {members.map((member) => (
                <MemberRow
                    key={member.id}
                    member={member}
                    avatarSize={avatarSize}
                    isCurrentUser={member.id === currentUserId}
                    showYouBadge={showYouBadge}
                    trailing={actions && (
                        <MemberRowMenu
                            member={member}
                            isCurrentUser={member.id === currentUserId}
                            disabled={actions.disabled}
                            onRemove={() => confirmRemove(member)}
                            onLeave={confirmLeave}
                        />
                    )}
                />
            ))}
        </div>
    );
};

export default MemberList;
