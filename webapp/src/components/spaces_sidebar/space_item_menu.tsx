// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import BellOutlineIcon from '@mattermost/compass-icons/components/bell-outline';
import CogOutlineIcon from '@mattermost/compass-icons/components/cog-outline';
import DotsVerticalIcon from '@mattermost/compass-icons/components/dots-vertical';
import ExitToAppIcon from '@mattermost/compass-icons/components/exit-to-app';
import LinkVariantIcon from '@mattermost/compass-icons/components/link-variant';
import StarIcon from '@mattermost/compass-icons/components/star';
import StarOutlineIcon from '@mattermost/compass-icons/components/star-outline';

import Menu from 'components/menu/menu';
import type {MenuItemSpec} from 'components/menu/menu_types';

import type {Space} from 'types/docs';

type Props = {
    space: Space;
    favorite: boolean;
    onToggleFavorite: (id: string) => void;
};

const SpaceItemMenu = ({space, favorite, onToggleFavorite}: Props) => {
    const {formatMessage} = useIntl();

    const items: MenuItemSpec[] = [
        {
            id: 'favorite',
            label: favorite ?
                formatMessage({id: 'docs.sidebar.space.unfavorite', defaultMessage: 'Remove from favorites'}) :
                formatMessage({id: 'docs.sidebar.space.favorite', defaultMessage: 'Add to favorites'}),
            leadingIcon: favorite ? <StarIcon size={18}/> : <StarOutlineIcon size={18}/>,
            onClick: () => onToggleFavorite(space.id),
        },
        {
            id: 'mute',
            label: formatMessage({id: 'docs.sidebar.space.mute', defaultMessage: 'Mute space'}),
            leadingIcon: <BellOutlineIcon size={18}/>,
        },
        {
            id: 'copy-link',
            label: formatMessage({id: 'docs.sidebar.space.copyLink', defaultMessage: 'Copy link'}),
            leadingIcon: <LinkVariantIcon size={18}/>,
        },
        {
            id: 'settings',
            label: formatMessage({id: 'docs.sidebar.space.settings', defaultMessage: 'Space settings'}),
            leadingIcon: <CogOutlineIcon size={18}/>,
        },
        {
            id: 'leave',
            label: formatMessage({id: 'docs.sidebar.space.leave', defaultMessage: 'Leave space'}),
            leadingIcon: <ExitToAppIcon size={18}/>,
            isDestructive: true,
            hasDivider: true,
        },
    ];

    const menuLabel = formatMessage({id: 'docs.sidebar.space.menu', defaultMessage: 'Space options for {name}'}, {name: space.name});

    return (
        <Menu
            ariaLabel={menuLabel}
            align='right'
            items={items}
            tooltip={menuLabel}
            trigger={(
                <button
                    type='button'
                    className='DocsSpaceItem__dot'
                    aria-label={menuLabel}
                    onClick={(e) => e.stopPropagation()}
                >
                    <DotsVerticalIcon size={16}/>
                </button>
            )}
        />
    );
};

export default SpaceItemMenu;
