// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/use_docs_navigation';
import {useTeamContext} from 'hooks/use_team_context';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import CreateSpaceButton from './create_space_button';
import type {DndCategory} from './dnd/types';
import FavoritesEmptyState from './favorites_empty_state';
import SpaceItem from './space_item';
import SpacesCategory from './spaces_category';
import SpacesSidebarHeader from './spaces_sidebar_header';
import SpacesSidebarNav, {type DocsNavKey} from './spaces_sidebar_nav';
import SpacesSidebarSearch from './spaces_sidebar_search';
import {useSidebarSpaces} from './use_sidebar_spaces';
import './spaces_sidebar.scss';

type Props = {
    onOpenSwitcher: () => void;
};

const SpacesSidebar = ({onOpenSwitcher}: Props) => {
    const {formatMessage} = useIntl();
    const {teamName} = useTeamContext();
    const {spaceId: selectedSpaceId, goToSpace, goHome} = useDocsNavigation();
    const {spacesById, favoriteOrder, spacesOrder, favoritesCollapsed, toggleFavoritesCollapsed, toggleFavorite} = useSidebarSpaces();
    const [activeNav, setActiveNav] = useState<DocsNavKey>('home');

    const selectNav = (key: DocsNavKey) => {
        setActiveNav(key);
        goHome();
    };

    const renderSpace = (id: string, category: DndCategory) => {
        const space = spacesById.get(id);
        if (!space) {
            return null;
        }
        return (
            <SpaceItem
                key={space.id}
                space={space}
                category={category}
                active={selectedSpaceId === space.id}
                favorite={category === 'favorites'}
                onSelect={goToSpace}
                onToggleFavorite={toggleFavorite}
            />
        );
    };

    return (
        <nav
            className='DocsSidebar'
            aria-label={formatMessage({id: 'docs.sidebar.label', defaultMessage: 'Spaces'})}
        >
            <div className='DocsSidebar__headerNav'>
                <SpacesSidebarHeader teamName={teamName}/>
                <SpacesSidebarSearch onOpen={onOpenSwitcher}/>
            </div>
            <SpacesSidebarNav
                active={selectedSpaceId ? null : activeNav}
                onSelect={selectNav}
            />
            <div className='DocsSidebar__scroll'>
                <SpacesCategory
                    title={formatMessage({id: 'docs.sidebar.category.favorites', defaultMessage: 'Favorites'})}
                    collapsible={true}
                    collapsed={favoritesCollapsed}
                    onToggle={toggleFavoritesCollapsed}
                >
                    {favoriteOrder.length === 0 ? <FavoritesEmptyState/> : favoriteOrder.map((id) => renderSpace(id, 'favorites'))}
                </SpacesCategory>
                <SpacesCategory
                    title={formatMessage({id: 'docs.sidebar.category.spaces', defaultMessage: 'Spaces'})}
                    collapsible={false}
                >
                    {spacesOrder.map((id) => renderSpace(id, 'spaces'))}
                    <CreateSpaceButton/>
                </SpacesCategory>
            </div>
        </nav>
    );
};

export default SpacesSidebar;
