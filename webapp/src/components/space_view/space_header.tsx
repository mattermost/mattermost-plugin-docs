// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useSpaceFavoriteState, useToggleFavorite} from 'hooks/favorites';
import {useLeaveSpace} from 'hooks/leave_space';
import {useDocsNavigation} from 'hooks/navigation';
import {useAppDispatch} from 'hooks/redux';
import React, {useCallback} from 'react';
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
import StarIcon from '@mattermost/compass-icons/components/star';
import StarOutlineIcon from '@mattermost/compass-icons/components/star-outline';

import {deleteSpace} from 'store/actions';
import {useSpacePermissions} from 'store/permissions';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import {Button, PrimaryButton} from 'components/form_controls/button';
import Header from 'components/header/header';
import Menu from 'components/menu/menu';
import {openDocsModal} from 'components/modals';
import ShareSpaceModal from 'components/share_space_modal/share_space_modal';
import SpaceSettingsModal from 'components/space_settings_modal/space_settings_modal';
import {toast} from 'components/toast';

import type {Space} from 'types/docs';

import styles from './space_header.module.scss';

type Props = {
    space: Space;
    memberCount?: number;
    infoOpen: boolean;
    onToggleInfo: () => void;

    /** Opens the space info panel on its members view. */
    onShowMembers: () => void;
};

const SpaceHeader = ({space, memberCount, infoOpen, onToggleInfo, onShowMembers}: Props) => {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();
    const {paths, spaceId, goHome} = useDocsNavigation();
    const {canManageMembers} = useSpacePermissions(space.id);
    const leaveThisSpace = useLeaveSpace(space);
    const favoriteState = useSpaceFavoriteState(space.id);
    const toggleFavorite = useToggleFavorite();
    const favorited = favoriteState === 'on';

    // `partial` means the space isn't favorited but holds favorited pages, so the
    // action offered is still "favorite this space".
    const favoriteLabel = {
        on: formatMessage({id: 'docs.space.unfavorite', defaultMessage: 'Remove from favorites'}),
        partial: formatMessage({id: 'docs.space.favoritePartial', defaultMessage: 'Favorite this space (some pages are favorited)'}),
        off: formatMessage({id: 'docs.space.favorite', defaultMessage: 'Favorite this space'}),
    }[favoriteState];
    const menuLabel = formatMessage({id: 'docs.space.menu', defaultMessage: 'Space options'});
    const infoLabel = formatMessage({id: 'docs.space.details', defaultMessage: 'Space details'});
    const membersLabel = formatMessage({id: 'docs.space.membersButton', defaultMessage: 'Members'});

    const copyLink = useCallback(() => {
        copyToClipboard(`${window.location.origin}${paths.space(space.id)}`);
    }, [paths, space.id]);

    const openShare = useCallback(() => {
        openDocsModal((modal) => (
            <ShareSpaceModal
                space={space}
                onClose={modal.close}
            />
        ));
    }, [space]);

    const openSettings = useCallback(() => {
        openDocsModal((modal) => (
            <SpaceSettingsModal
                space={space}
                onClose={modal.close}
            />
        ));
    }, [space]);

    const openLeaveConfirm = useCallback(() => {
        openDocsModal((modal) => (
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
                onConfirm={async () => {
                    await leaveThisSpace();
                    modal.close();
                }}
                onCancel={modal.close}
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
        ));
    }, [space.title, leaveThisSpace]);

    const openArchiveConfirm = useCallback(() => {
        openDocsModal((modal) => (
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
                onConfirm={async () => {
                    try {
                        await dispatch(deleteSpace(space.id));
                        if (spaceId === space.id) {
                            goHome();
                        }
                    } catch (error) {
                        // Without this, a failed archive looks exactly like a
                        // successful one: the modal just closes.
                        toast.error(
                            formatMessage({
                                id: 'docs.archiveSpace.error.title',
                                defaultMessage: 'Unable to archive {name}',
                            }, {name: space.title}),
                            {
                                description: formatMessage({
                                    id: 'docs.archiveSpace.error.generic',
                                    defaultMessage: 'Something went wrong. Please try again.',
                                }),
                            },
                        );

                        // eslint-disable-next-line no-console
                        console.error('Docs: failed to archive space', error);
                    }
                    modal.close();
                }}
                onCancel={modal.close}
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
        ));
    }, [dispatch, space.id, space.title, spaceId, goHome, formatMessage]);

    // The space title names the trigger, so it carries no aria-label; the menu
    // itself is named by `ariaLabel` on the popup.
    const left = (
        <div className={styles.leftCluster}>
            <Button
                emphasis='quaternary'
                size='sm'
                className={classNames('btn-icon', {active: favoriteState !== 'off'})}
                tooltip={favoriteLabel}
                aria-pressed={favoriteState === 'partial' ? 'mixed' : favorited}
                onClick={() => toggleFavorite('space', space.id)}
            >
                {favorited ? <StarIcon size={18}/> : <StarOutlineIcon size={18}/>}
            </Button>
            <Menu
                ariaLabel={menuLabel}
                trigger={(
                    <Button
                        emphasis='quaternary'
                        size='sm'
                        className={styles.titleTrigger}
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
                            aria-hidden={true}
                        />
                    </Button>
                )}
            >
                <Menu.Item
                    leadingIcon={<InformationOutlineIcon size={18}/>}
                    onClick={onToggleInfo}
                >
                    <FormattedMessage
                        id='docs.space.menu.info'
                        defaultMessage='Space info'
                    />
                </Menu.Item>
                <Menu.Item
                    leadingIcon={<AccountMultipleOutlineIcon size={18}/>}
                    onClick={onShowMembers}
                >
                    <FormattedMessage
                        id='docs.space.menu.members'
                        defaultMessage='Members'
                    />
                </Menu.Item>
                <Menu.Item
                    leadingIcon={<LinkVariantIcon size={18}/>}
                    onClick={copyLink}
                >
                    <FormattedMessage
                        id='docs.space.menu.copyLink'
                        defaultMessage='Copy link'
                    />
                </Menu.Item>
                {canManageMembers && (
                    <Menu.Item
                        leadingIcon={<CogOutlineIcon size={18}/>}
                        onClick={openSettings}
                    >
                        <FormattedMessage
                            id='docs.space.menu.settings'
                            defaultMessage='Space settings'
                        />
                    </Menu.Item>
                )}
                <Menu.Separator/>
                <Menu.Item
                    leadingIcon={<ExitToAppIcon size={18}/>}
                    destructive={true}
                    onClick={openLeaveConfirm}
                >
                    <FormattedMessage
                        id='docs.space.menu.leave'
                        defaultMessage='Leave space'
                    />
                </Menu.Item>
                {canManageMembers && (
                    <Menu.Item
                        leadingIcon={<ArchiveOutlineIcon size={18}/>}
                        destructive={true}
                        onClick={openArchiveConfirm}
                    >
                        <FormattedMessage
                            id='docs.space.menu.archive'
                            defaultMessage='Archive space'
                        />
                    </Menu.Item>
                )}
            </Menu>
            <Button
                emphasis='quaternary'
                size='sm'
                className={classNames('docs-btn-neutral', styles.members)}
                tooltip={membersLabel}
                onClick={onShowMembers}
            >
                <AccountMultipleOutlineIcon size={16}/>
                {memberCount !== undefined && <span>{memberCount}</span>}
            </Button>
        </div>
    );

    const right = (
        <>
            <Button
                emphasis='quaternary'
                size='sm'
                className={classNames('btn-icon', {active: infoOpen})}
                tooltip={infoLabel}
                aria-pressed={infoOpen}
                onClick={onToggleInfo}
            >
                <InformationOutlineIcon size={18}/>
            </Button>
            <PrimaryButton
                size='sm'
                className={styles.share}
                onClick={openShare}
            >
                <ShareVariantOutlineIcon size={16}/>
                <FormattedMessage
                    id='docs.space.share'
                    defaultMessage='Share'
                />
            </PrimaryButton>
        </>
    );

    return (
        <Header
            className={styles.header}
            left={left}
            right={right}
        />
    );
};

export default SpaceHeader;
