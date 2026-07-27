// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';

import DotsVerticalIcon from '@mattermost/compass-icons/components/dots-vertical';
import ExitToAppIcon from '@mattermost/compass-icons/components/exit-to-app';
import LinkVariantIcon from '@mattermost/compass-icons/components/link-variant';

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
    const {paths} = useDocsNavigation();

    const [confirmLeaveOpen, setConfirmLeaveOpen] = useState(false);

    const copyLink = () => copyToClipboard(`${window.location.origin}${paths.space(space.id)}`);

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
                    onConfirm={() => setConfirmLeaveOpen(false)}
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
