// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useAppDispatch} from 'hooks/redux';
import React, {useCallback, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';

import DotsVerticalIcon from '@mattermost/compass-icons/components/dots-vertical';
import ExitToAppIcon from '@mattermost/compass-icons/components/exit-to-app';
import LinkVariantIcon from '@mattermost/compass-icons/components/link-variant';

import {leaveSpace} from 'store/actions';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import Menu from 'components/menu/menu';
import type {MenuItemSpec} from 'components/menu/menu_types';

import type {Space} from 'types/docs';

import styles from './space_item.module.scss';

type Props = {
    space: Space;
};

const SpaceItemMenu = ({space}: Props) => {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();
    const {paths, spaceId, goHome} = useDocsNavigation();

    const [confirmLeaveOpen, setConfirmLeaveOpen] = useState(false);

    const copyLink = () => copyToClipboard(`${window.location.origin}${paths.space(space.id)}`);

    // Leaving removes the current user's membership. Navigate home only if we
    // just left the space being viewed, and only after the server confirms
    // (a last-authorized-member removal is rejected with 409).
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

    const items: MenuItemSpec[] = [

        // Favorite space is deferred until ordering/favorites persist to user
        // preferences (spec §4, Phase B).
        // {
        //     id: 'favorite',
        //     label: <FormattedMessage id='docs.sidebar.space.favorite' defaultMessage='Add to favorites'/>,
        //     leadingIcon: <StarOutlineIcon size={18}/>,
        //     onClick: () => onToggleFavorite(space.id),
        // },

        // Mute space is deferred until the mute feature exists.
        // {
        //     id: 'mute',
        //     label: formatMessage({id: 'docs.sidebar.space.mute', defaultMessage: 'Mute space'}),
        //     leadingIcon: <BellOutlineIcon size={18}/>,
        // },
        {
            id: 'copy-link',
            label: (
                <FormattedMessage
                    id='docs.sidebar.space.copyLink'
                    defaultMessage='Copy link'
                />
            ),
            leadingIcon: <LinkVariantIcon size={18}/>,
            onClick: copyLink,
        },

        // Space settings is deferred until the settings feature exists.
        // {
        //     id: 'settings',
        //     label: formatMessage({id: 'docs.sidebar.space.settings', defaultMessage: 'Space settings'}),
        //     leadingIcon: <CogOutlineIcon size={18}/>,
        // },
        {
            id: 'leave',
            label: (
                <FormattedMessage
                    id='docs.sidebar.space.leave'
                    defaultMessage='Leave space'
                />
            ),
            leadingIcon: <ExitToAppIcon size={18}/>,
            isDestructive: true,
            hasDivider: true,
            onClick: () => setConfirmLeaveOpen(true),
        },
    ];

    const menuLabel = formatMessage({id: 'docs.sidebar.space.menu', defaultMessage: 'Space options for {name}'}, {name: space.title});

    return (
        <>
            <Menu
                ariaLabel={menuLabel}
                align='right'
                items={items}
                tooltip={menuLabel}
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
            />
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
