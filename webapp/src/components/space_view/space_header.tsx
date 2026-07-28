// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useDocsNavigation} from 'hooks/navigation';
import {useAppDispatch} from 'hooks/redux';
import React, {useCallback, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';
import {SpaceIcon} from 'utils/space_icon';

import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import ArchiveOutlineIcon from '@mattermost/compass-icons/components/archive-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import CogOutlineIcon from '@mattermost/compass-icons/components/cog-outline';
import ExitToAppIcon from '@mattermost/compass-icons/components/exit-to-app';
import InformationOutlineIcon from '@mattermost/compass-icons/components/information-outline';
import LinkVariantIcon from '@mattermost/compass-icons/components/link-variant';
import ShareVariantOutlineIcon from '@mattermost/compass-icons/components/share-variant-outline';
import StarOutlineIcon from '@mattermost/compass-icons/components/star-outline';

import {deleteSpace, leaveSpace} from 'store/actions';
import {useSpacePermissions} from 'store/permissions';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import {Button, PrimaryButton} from 'components/form_controls/button';
import Menu from 'components/menu/menu';
import type {MenuItemSpec} from 'components/menu/menu_types';
import ShareSpaceModal from 'components/share_space_modal/share_space_modal';

import type {Space} from 'types/docs';

import styles from './space_header.module.scss';

type Props = {
    space: Space;
    memberCount: number;
    infoOpen: boolean;
    onToggleInfo: () => void;
    onOpenSettings: () => void;
};

const SpaceHeader = ({space, memberCount, infoOpen, onToggleInfo, onOpenSettings}: Props) => {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();
    const {paths, spaceId, goHome} = useDocsNavigation();
    const {canManageMembers} = useSpacePermissions(space.id);

    const [shareOpen, setShareOpen] = useState(false);
    const [confirmLeaveOpen, setConfirmLeaveOpen] = useState(false);
    const [confirmArchiveOpen, setConfirmArchiveOpen] = useState(false);

    const favoriteLabel = formatMessage({id: 'docs.space.favorite', defaultMessage: 'Favorite this space'});
    const menuLabel = formatMessage({id: 'docs.space.menu', defaultMessage: 'Space options'});
    const infoLabel = formatMessage({id: 'docs.space.details', defaultMessage: 'Space details'});
    const membersLabel = formatMessage({id: 'docs.space.membersButton', defaultMessage: 'Members'});

    const copyLink = useCallback(() => {
        copyToClipboard(`${window.location.origin}${paths.space(space.id)}`);
    }, [paths, space.id]);

    // Navigate home only when we just acted on the space being viewed, and only
    // after the server confirms (leaving the last authorized member is rejected).
    const confirmLeave = useCallback(async () => {
        try {
            await dispatch(leaveSpace(space.id));
            if (spaceId === space.id) {
                goHome();
            }
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to leave space', error);
        }
        setConfirmLeaveOpen(false);
    }, [dispatch, space.id, spaceId, goHome]);

    const confirmArchive = useCallback(async () => {
        try {
            await dispatch(deleteSpace(space.id));
            if (spaceId === space.id) {
                goHome();
            }
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to archive space', error);
        }
        setConfirmArchiveOpen(false);
    }, [dispatch, space.id, spaceId, goHome]);

    const items: MenuItemSpec[] = [
        {
            id: 'info',
            label: (
                <FormattedMessage
                    id='docs.space.menu.info'
                    defaultMessage='Space info'
                />
            ),
            leadingIcon: <InformationOutlineIcon size={18}/>,
            onClick: onToggleInfo,
        },
        {
            id: 'members',
            label: (
                <FormattedMessage
                    id='docs.space.menu.members'
                    defaultMessage='Members'
                />
            ),
            leadingIcon: <AccountMultipleOutlineIcon size={18}/>,
            onClick: () => setShareOpen(true),
        },
        {
            id: 'copy-link',
            label: (
                <FormattedMessage
                    id='docs.space.menu.copyLink'
                    defaultMessage='Copy link'
                />
            ),
            leadingIcon: <LinkVariantIcon size={18}/>,
            onClick: copyLink,
        },
        ...(canManageMembers ? [{
            id: 'settings',
            label: (
                <FormattedMessage
                    id='docs.space.menu.settings'
                    defaultMessage='Space settings'
                />
            ),
            leadingIcon: <CogOutlineIcon size={18}/>,
            onClick: onOpenSettings,
        }] : []),
        {
            id: 'leave',
            label: (
                <FormattedMessage
                    id='docs.space.menu.leave'
                    defaultMessage='Leave space'
                />
            ),
            leadingIcon: <ExitToAppIcon size={18}/>,
            hasDivider: true,
            onClick: () => setConfirmLeaveOpen(true),
        },
        ...(canManageMembers ? [{
            id: 'archive',
            label: (
                <FormattedMessage
                    id='docs.space.menu.archive'
                    defaultMessage='Archive space'
                />
            ),
            leadingIcon: <ArchiveOutlineIcon size={18}/>,
            isDestructive: true,
            onClick: () => setConfirmArchiveOpen(true),
        }] : []),
    ];

    return (
        <div className={styles.bar}>
            <div className={styles.left}>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='xs'
                    className='btn-icon'
                    aria-label={favoriteLabel}
                >
                    <StarOutlineIcon size={18}/>
                </Button>
                <Menu
                    ariaLabel={menuLabel}
                    tooltip={menuLabel}
                    items={items}
                    trigger={(
                        <Button
                            type='button'
                            emphasis='quaternary'
                            size='sm'
                            className={styles.titleTrigger}
                            aria-label={menuLabel}
                        >
                            <span
                                className={styles.emoji}
                                aria-hidden={true}
                            >
                                <SpaceIcon
                                    space={space}
                                    size={18}
                                />
                            </span>
                            <span className={styles.title}>{space.title}</span>
                            <ChevronDownIcon
                                className={styles.chevron}
                                size={16}
                            />
                        </Button>
                    )}
                />
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='xs'
                    className={classNames('docs-btn-neutral', styles.members)}
                    aria-label={membersLabel}
                    onClick={() => setShareOpen(true)}
                >
                    <AccountMultipleOutlineIcon size={16}/>
                    <span>{memberCount}</span>
                </Button>
            </div>

            <div className={styles.right}>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className={classNames('btn-icon', {active: infoOpen})}
                    aria-label={infoLabel}
                    aria-pressed={infoOpen}
                    onClick={onToggleInfo}
                >
                    <InformationOutlineIcon size={18}/>
                </Button>
                <PrimaryButton
                    type='button'
                    size='sm'
                    className={styles.share}
                    onClick={() => setShareOpen(true)}
                >
                    <ShareVariantOutlineIcon size={16}/>
                    <FormattedMessage
                        id='docs.space.share'
                        defaultMessage='Share'
                    />
                </PrimaryButton>
            </div>
            {shareOpen && (
                <ShareSpaceModal
                    space={space}
                    onClose={() => setShareOpen(false)}
                />
            )}
            {confirmLeaveOpen && (
                <ConfirmModal
                    title={(
                        <FormattedMessage
                            id='docs.leaveSpace.title'
                            defaultMessage='Leave {name}'
                            values={{name: space.title}}
                        />
                    )}
                    confirmButtonText={(
                        <FormattedMessage
                            id='docs.leaveSpace.confirm'
                            defaultMessage='Yes, leave space'
                        />
                    )}
                    isConfirmDestructive={true}
                    onConfirm={confirmLeave}
                    onCancel={() => setConfirmLeaveOpen(false)}
                >
                    <FormattedMessage
                        id='docs.leaveSpace.message'
                        defaultMessage='Are you sure you want to leave the <b>{name}</b> space? You can rejoin later if it is public.'
                        values={{
                            name: space.title,
                            b: (chunks) => <b>{chunks}</b>,
                        }}
                    />
                </ConfirmModal>
            )}
            {confirmArchiveOpen && (
                <ConfirmModal
                    title={(
                        <FormattedMessage
                            id='docs.archiveSpace.title'
                            defaultMessage='Archive {name}'
                            values={{name: space.title}}
                        />
                    )}
                    confirmButtonText={(
                        <FormattedMessage
                            id='docs.archiveSpace.confirm'
                            defaultMessage='Yes, archive space'
                        />
                    )}
                    isConfirmDestructive={true}
                    onConfirm={confirmArchive}
                    onCancel={() => setConfirmArchiveOpen(false)}
                >
                    <FormattedMessage
                        id='docs.archiveSpace.message'
                        defaultMessage='Are you sure you want to archive the <b>{name}</b> space? Members will lose access until it is restored.'
                        values={{
                            name: space.title,
                            b: (chunks) => <b>{chunks}</b>,
                        }}
                    />
                </ConfirmModal>
            )}
        </div>
    );
};

export default SpaceHeader;
