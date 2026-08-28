// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useCustomDefaultsAvailable} from 'hooks/permissions';
import {useSpaceAccessEditor} from 'hooks/space_access_editor';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';

import ContentCopyIcon from '@mattermost/compass-icons/components/content-copy';

import {SecondaryButton} from 'components/form_controls/button';
import GenericModal from 'components/generic_modal/generic_modal';
import {AddMembersField, MemberList} from 'components/space_members';

import type {Space} from 'types/docs';
import {MEMBER_PERMISSION_ORDER, Permissions, type Permission} from 'types/permissions';

import DefaultPermissionsMenu from './default_permissions_menu';
import styles from './share_space_modal.module.scss';
import VisibilityMenu from './visibility_menu';

type Props = {
    space: Space;
    onClose: () => void;
};

// Primary space-sharing surface. The compact controls preserve the approved Share modal: the
// named tiers come first, and a licensed install can refine the space default permission by
// permission beneath them.
const ShareSpaceModal = ({space, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const {paths: absolutePaths} = useDocsNavigation({absolute: true});
    const {
        permissions,
        members,
        memberIds,
        adminLocked,
        rosterLocked,
        adminLockedReason,
        adminSpaceLockedReason,
        roleForMember,
        memberLockedReason,
        isMemberLocked,
        actions,
        addMembers,
    } = useSpaceAccessEditor(space, {onClose});

    const customDefaultsAvailable = useCustomDefaultsAvailable();

    const copyLink = () => copyToClipboard(absolutePaths.space(space.id));

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
            onClick={copyLink}
        >
            <ContentCopyIcon size={16}/>
            <FormattedMessage
                id='docs.share.copyLink'
                defaultMessage='Copy link'
            />
        </SecondaryButton>
    );

    const footer = (
        <div className={styles.access}>
            <VisibilityMenu
                viewAccess={permissions.viewAccess}
                disabled={adminLocked}
                disabledReason={adminLockedReason}
                onChange={(next) => permissions.setViewAccess(next).catch(() => {})}
            />
            <DefaultPermissionsMenu
                defaults={permissions.defaults}
                disabled={adminLocked}
                disabledReason={adminLockedReason}
                customDefaultsAvailable={customDefaultsAvailable}
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
                            disabled={rosterLocked}
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
                            if (!record) {
                                return undefined;
                            }

                            return {
                                options: MEMBER_PERMISSION_ORDER,
                                selected: record.granted_permissions,
                                effective: record.permissions,
                                disabled: isMemberLocked(profile),
                                disabledReason: memberLockedReason(profile),
                                disabledOptions: permissions.canAdminister ? undefined : [Permissions.ADMIN_SPACE],
                                disabledOptionsReason: adminSpaceLockedReason,
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
