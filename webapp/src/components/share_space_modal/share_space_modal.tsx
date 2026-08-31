// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCopyText} from 'hooks/copy_text';
import {useSpaceMemberProfiles} from 'hooks/members';
import {useDocsNavigation} from 'hooks/navigation';
import {useCanManageSpaceMembers} from 'hooks/permissions';
import {useManageSpaceMembers} from 'hooks/space_members';
import React, {useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import CheckIcon from '@mattermost/compass-icons/components/check';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ContentCopyIcon from '@mattermost/compass-icons/components/content-copy';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

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
        onLeave: async () => {
            if (await leave()) {
                onClose();
            }
        },
        disabled: busy,
    };

    const copyLink = useCopyText(absolutePaths.space(space.id));

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
            aria-live='polite'
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

    const footer = (
        <div className={styles.access}>
            <div className={styles.accessLeft}>
                <button
                    type='button'
                    className={styles.accessTrigger}
                    disabled={true}
                    aria-haspopup='listbox'
                    title={formatMessage({
                        id: 'docs.share.visibility.disabledReason',
                        defaultMessage: 'Public spaces are coming soon',
                    })}
                >
                    <LockOutlineIcon size={16}/>
                    <FormattedMessage
                        id='docs.share.visibility.private'
                        defaultMessage='Private'
                    />
                    <ChevronDownIcon size={16}/>
                </button>
                <span className={styles.accessHint}>
                    <FormattedMessage
                        id='docs.share.visibility.privateHint'
                        defaultMessage='Only invited members'
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
            ariaLabel={formatMessage({id: 'docs.share.title', defaultMessage: "Share ''{name}''"}, {name: space.title})}
            onClose={onClose}
            footer={footer}
            footerClassName={styles.footer}
            headerDivider={false}
            footerDivider={true}
        >
            <div className={styles.body}>
                {canManageMembers && (
                    <div className={styles.search}>
                        <AddMembersField
                            excludeIds={memberIds}
                            onAdd={addMembers}
                            disabled={busy}
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
                    />
                </div>
            </div>
        </GenericModal>
    );
};

export default ShareSpaceModal;
