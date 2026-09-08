// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCopyText} from 'hooks/copy_text';
import {useDocsNavigation} from 'hooks/navigation';
import {useCustomDefaultsAvailable} from 'hooks/permissions';
import {useSpaceAccessEditor} from 'hooks/space_access_editor';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import CheckIcon from '@mattermost/compass-icons/components/check';
import ContentCopyIcon from '@mattermost/compass-icons/components/content-copy';

import {SecondaryButton} from 'components/form_controls/button';
import GenericModal from 'components/generic_modal/generic_modal';
import {AddMembersField, MemberList} from 'components/space_members';

import type {Space} from 'types/docs';
import type {Permission} from 'types/permissions';

import DefaultPermissionsMenu from './default_permissions_menu';
import styles from './share_space_modal.module.scss';
import VisibilityMenu from './visibility_menu';

type Props = {
    space: Space;
    onClose: () => void;
};

// Primary space-sharing surface. The compact controls preserve the approved Share modal: the
// named tiers come first, and a licensed install can refine the space default permission by
// permission beneath them. The tiers name the space default only; a member's row edits the
// permission ids themselves.
const ShareSpaceModal = ({space, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const {paths: absolutePaths} = useDocsNavigation({absolute: true});
    const {
        permissions,
        members,
        memberIds,
        canEditAccess,
        grantOptionsFor,
        accessBusy,
        rosterBusy,
        busyReason,
        roleForMember,
        actions,
        addMembers,
    } = useSpaceAccessEditor(space, {onClose});

    const customDefaultsAvailable = useCustomDefaultsAvailable();

    const copyLink = useCopyText(absolutePaths.space(space.id), {
        announcement: formatMessage({id: 'docs.share.linkCopied', defaultMessage: 'Copied'}),
    });

    const title = (
        <FormattedMessage
            id='docs.share.title'
            defaultMessage="Share ''{name}''"
            values={{name: space.title}}
        />
    );

    const titleActions = (
        <SecondaryButton
            size='sm'
            className={styles.copyLink}
            onClick={copyLink.copy}
        >
            {copyLink.copied ? <CheckIcon size={16}/> : <ContentCopyIcon size={16}/>}
            {copyLink.copied ? (
                <FormattedMessage
                    id='docs.share.linkCopied'
                    defaultMessage='Copied'
                />
            ) : (
                <FormattedMessage
                    id='docs.share.copyLink'
                    defaultMessage='Copy link'
                />
            )}
        </SecondaryButton>
    );

    // A caller who does not administer the space still reads its exposure here; what they lose
    // is the control, not the statement, since either write would be refused.
    const footer = (
        <div className={styles.access}>
            <VisibilityMenu
                viewAccess={permissions.viewAccess}
                disabled={accessBusy}
                disabledReason={busyReason}
                readOnly={!canEditAccess}
                onChange={(next) => permissions.setViewAccess(next).catch(() => {})}
            />
            <DefaultPermissionsMenu
                defaults={permissions.defaults}
                disabled={accessBusy}
                disabledReason={busyReason}
                customDefaultsAvailable={customDefaultsAvailable}
                readOnly={!canEditAccess}
                onChange={(next) => permissions.setDefaults(next).catch(() => {})}
            />
        </div>
    );

    return (
        <GenericModal
            className={styles.modal}
            title={title}
            titleActions={titleActions}
            ariaLabel={formatMessage({id: 'docs.share.title', defaultMessage: "Share ''{name}''"}, {name: space.title})}
            onClose={onClose}
            footer={footer}
            footerClassName={styles.footer}
            headerDivider={false}
            footerDivider={true}
        >
            <div className={styles.body}>
                {permissions.canManageMembers && (
                    <div className={styles.search}>
                        <AddMembersField
                            excludeIds={memberIds}
                            onAdd={addMembers}
                            disabled={rosterBusy}
                            large={true}
                            commitOnSelect={true}
                        />
                    </div>
                )}
                <div className={styles.members}>
                    <MemberList
                        members={members}
                        avatarSize='md'
                        showYouBadge={true}
                        spaceTitle={space.title}
                        comfortable={true}
                        actions={actions}
                        roleForMember={roleForMember}
                        permissionMenuForMember={(profile) => {
                            const record = permissions.members.get(profile.id);
                            const options = grantOptionsFor(profile);
                            if (!record || options.length === 0) {
                                return undefined;
                            }

                            return {
                                options,
                                selected: record.granted_permissions,
                                disabled: rosterBusy,
                                disabledReason: busyReason,
                                onChange: (next: Permission[]) => {
                                    permissions.setMemberGrants(profile.id, next).catch(() => {});
                                },
                            };
                        }}
                    />
                </div>
            </div>
        </GenericModal>
    );
};

export default ShareSpaceModal;
