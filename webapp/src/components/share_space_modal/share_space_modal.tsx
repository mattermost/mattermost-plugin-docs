// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useSpaceMemberProfiles} from 'hooks/members';
import {useDocsNavigation} from 'hooks/navigation';
import {useAppSelector} from 'hooks/redux';
import React, {useMemo, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';
import {Avatar} from 'webapp_globals';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ContentCopyIcon from '@mattermost/compass-icons/components/content-copy';
import GlobeIcon from '@mattermost/compass-icons/components/globe';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {useSpacePermissions} from 'store/permissions';

import {Button, SecondaryButton} from 'components/form_controls/button';
import GenericModal from 'components/generic_modal/generic_modal';

import type {Space} from 'types/docs';

import PeoplePicker from './people_picker';
import styles from './share_space_modal.module.scss';

type Props = {
    space: Space;
    onClose: () => void;
};

// Members come from the real member API and Copy link is functional. Roles and
// space visibility (Admin / Public / Can View) are capability/view-access
// features from PR #10, so those dropdowns are visual scaffolding.
//
// Adding people needs a server add-member API (also PR #10). The people-search
// combobox and its live search pipeline are built, but gated on the
// canManageMembers permission so we don't ship an "add people" control whose
// selections can't persist (they'd only live client-side).
const ShareSpaceModal = ({space, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const {paths} = useDocsNavigation();
    const members = useSpaceMemberProfiles(space.id);
    const currentUserId = useAppSelector(getCurrentUserId);
    const {canManageMembers} = useSpacePermissions(space.id);

    // People chosen from the search picker. There's no add-member API yet
    // (roles/view-access land with PR #10), so they stay as pending chips in the
    // picker rather than joining the member list.
    const [pending, setPending] = useState<MemberProfile[]>([]);

    const excludeIds = useMemo(
        () => [...members.map((member) => member.id), ...pending.map((user) => user.id)],
        [members, pending],
    );

    const copyLink = () => copyToClipboard(`${window.location.origin}${paths.space(space.id)}`);

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
                    <PeoplePicker
                        selected={pending}
                        excludeIds={excludeIds}
                        onChange={setPending}
                    />
                )}
                <div className={styles.memberList}>
                    {members.map((member) => (
                        <div
                            key={member.id}
                            className={styles.memberRow}
                        >
                            <Avatar
                                url={member.avatarUrl}
                                username={member.username}
                                size='md'
                                name=''
                            />
                            <span className={styles.memberInfo}>
                                <span className={styles.memberName}>{member.displayName}</span>
                                {member.username && (
                                    <span className={styles.memberUsername}>
                                        <FormattedMessage
                                            id='docs.share.handle'
                                            defaultMessage='@{username}'
                                            values={{username: member.username}}
                                        />
                                    </span>
                                )}
                                {member.id === currentUserId && (
                                    <span className={styles.you}>
                                        <FormattedMessage
                                            id='docs.share.you'
                                            defaultMessage='(You)'
                                        />
                                    </span>
                                )}
                            </span>
                            <Button
                                type='button'
                                emphasis='quaternary'
                                size='sm'
                                className={styles.roleTrigger}
                            >
                                <FormattedMessage
                                    id='docs.share.role.admin'
                                    defaultMessage='Admin'
                                />
                                <ChevronDownIcon size={16}/>
                            </Button>
                        </div>
                    ))}
                </div>
            </div>
        </GenericModal>
    );
};

export default ShareSpaceModal;
