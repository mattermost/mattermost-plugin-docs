// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceFavoriteState, useToggleFavorite} from 'hooks/favorites';
import {useLeaveSpace} from 'hooks/leave_space';
import {useDocsNavigation} from 'hooks/navigation';
import React, {useCallback, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';

import DotsVerticalIcon from '@mattermost/compass-icons/components/dots-vertical';
import DownloadOutlineIcon from '@mattermost/compass-icons/components/download-outline';
import ExitToAppIcon from '@mattermost/compass-icons/components/exit-to-app';
import LinkVariantIcon from '@mattermost/compass-icons/components/link-variant';
import StarIcon from '@mattermost/compass-icons/components/star';
import StarOutlineIcon from '@mattermost/compass-icons/components/star-outline';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import Menu from 'components/menu/menu';

import type {Space} from 'types/docs';

import styles from './space_item.module.scss';

type Props = {
    space: Space;
};

const SpaceItemMenu = ({space}: Props) => {
    const {formatMessage} = useIntl();
    const {paths: absolutePaths, goToImport} = useDocsNavigation({absolute: true});
    const leaveThisSpace = useLeaveSpace(space);
    const favoriteState = useSpaceFavoriteState(space.id);
    const toggleFavorite = useToggleFavorite();
    const favorited = favoriteState === 'on';

    const [confirmLeaveOpen, setConfirmLeaveOpen] = useState(false);

    const copyLink = () => copyToClipboard(absolutePaths.space(space.id));

    const confirmLeave = useCallback(async () => {
        await leaveThisSpace();
        setConfirmLeaveOpen(false);
    }, [leaveThisSpace]);

    // The accessible name carries the space, since a screen reader reaching this
    // button has no row to read it from. The tooltip drops it: the pointer is already
    // resting on the row, so repeating its name there is noise.
    const menuLabel = formatMessage({id: 'docs.sidebar.space.menu', defaultMessage: 'Space options for {name}'}, {name: space.title});
    const tooltipLabel = formatMessage({id: 'docs.sidebar.space.menuShort', defaultMessage: 'Space options'});

    return (
        <>
            <Menu
                ariaLabel={menuLabel}
                align='right'
                tooltip={tooltipLabel}
                trigger={(
                    <button
                        type='button'
                        className={styles.dot}
                        aria-label={menuLabel}
                        onClick={(e) => e.stopPropagation()}
                    >
                        <DotsVerticalIcon size={16}/>
                    </button>
                )}
            >
                {/* Mute is deferred until a mute feature exists; space settings
                  * until that surface lands. */}
                <Menu.Item
                    leadingIcon={favorited ? <StarIcon size={18}/> : <StarOutlineIcon size={18}/>}
                    secondaryLabel={favoriteState === 'partial' ? (
                        <FormattedMessage
                            id='docs.sidebar.space.favoritePartial'
                            defaultMessage='Some pages are favorited'
                        />
                    ) : undefined}
                    onClick={() => toggleFavorite('space', space.id)}
                >
                    {favorited ? (
                        <FormattedMessage
                            id='docs.sidebar.space.unfavorite'
                            defaultMessage='Remove from favorites'
                        />
                    ) : (
                        <FormattedMessage
                            id='docs.sidebar.space.favorite'
                            defaultMessage='Add to favorites'
                        />
                    )}
                </Menu.Item>
                <Menu.Item
                    leadingIcon={<LinkVariantIcon size={18}/>}
                    onClick={copyLink}
                >
                    <FormattedMessage
                        id='docs.sidebar.space.copyLink'
                        defaultMessage='Copy link'
                    />
                </Menu.Item>

                {/* Importing into a Space that already exists is a decision about *this* Space — which of its
                  * pages the bundle adopts, and whose edits an overwrite would discard — so it is offered here
                  * rather than only from the "new Space" entry in the sidebar. */}
                <Menu.Item
                    leadingIcon={<DownloadOutlineIcon size={18}/>}
                    onClick={() => goToImport(space.id)}
                >
                    <FormattedMessage
                        id='docs.sidebar.space.import'
                        defaultMessage='Import from Confluence'
                    />
                </Menu.Item>
                <Menu.Separator/>
                <Menu.Item
                    leadingIcon={<ExitToAppIcon size={18}/>}
                    destructive={true}
                    onClick={() => setConfirmLeaveOpen(true)}
                >
                    <FormattedMessage
                        id='docs.sidebar.space.leave'
                        defaultMessage='Leave space'
                    />
                </Menu.Item>
            </Menu>
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
        </>
    );
};

export default SpaceItemMenu;
