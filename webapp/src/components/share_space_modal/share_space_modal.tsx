// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceMemberProfiles} from 'hooks/members';
import {useDocsNavigation} from 'hooks/navigation';
import {useCanManageSpaceMembers} from 'hooks/permissions';
import {useManageSpaceMembers} from 'hooks/space_members';
import React, {useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ContentCopyIcon from '@mattermost/compass-icons/components/content-copy';
import GlobeIcon from '@mattermost/compass-icons/components/globe';

import {Button, SecondaryButton} from 'components/form_controls/button';
import GenericModal from 'components/generic_modal/generic_modal';
import {AddMembersField, MemberList} from 'components/space_members';
import type {MemberListActions} from 'components/space_members';

import type {Space} from 'types/docs';

import styles from './share_space_modal.module.scss';

type Props = {
    space: Space;
    onClose: () => void;
};

// Members, add and remove are real. The visibility and role dropdowns are
// scaffolding for PR #10's view_access and capabilities.
const ShareSpaceModal = ({space, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const {paths: absolutePaths} = useDocsNavigation({absolute: true});
    const members = useSpaceMemberProfiles(space.id);
    const canManageMembers = useCanManageSpaceMembers(space.id);
    const {addMembers, removeMember, leave, busy} = useManageSpaceMembers(space);

    const memberIds = useMemo(() => members.map((member) => member.id), [members]);

    const actions: MemberListActions = {
        ...(canManageMembers && {onRemove: removeMember}),
        onLeave: () => leave().then(onClose),
        disabled: busy,
    };

    const copyLink = () => copyToClipboard(absolutePaths.space(space.id));

    const title = (
        <FormattedMessage
            id='docs.share.title'
            defaultMessage='Share space'
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
            <div className={styles.accessLeft}>
                <GlobeIcon size={20}/>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className={styles.accessTrigger}
                >
                    <FormattedMessage
                        id='docs.share.visibility.public'
                        defaultMessage='Public'
                    />
                    <ChevronDownIcon size={16}/>
                </Button>
                <span className={styles.accessHint}>
                    <FormattedMessage
                        id='docs.share.visibility.publicHint'
                        defaultMessage='Anyone in Mattermost'
                    />
                </span>
            </div>
            <Button
                type='button'
                emphasis='quaternary'
                size='sm'
                className={styles.canView}
            >
                <FormattedMessage
                    id='docs.share.access.canView'
                    defaultMessage='Can View'
                />
                <ChevronDownIcon size={16}/>
            </Button>
        </div>
    );

    return (
        <GenericModal
            className={styles.modal}
            title={title}
            titleActions={titleActions}
            ariaLabel={formatMessage({id: 'docs.share.title', defaultMessage: 'Share space'})}
            onClose={onClose}
            footer={footer}
        >
            <div className={styles.body}>
                {canManageMembers && (
                    <AddMembersField
                        excludeIds={memberIds}
                        onAdd={addMembers}
                        disabled={busy}
                    />
                )}
                <MemberList
                    members={members}
                    avatarSize='md'
                    showYouBadge={true}
                    spaceTitle={space.title}
                    actions={actions}
                />
            </div>
        </GenericModal>
    );
};

export default ShareSpaceModal;
