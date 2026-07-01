// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useTeamContext} from 'hooks/team';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import CreateSpaceButton from './create_space_button';
import type {DndCategory} from './dnd/types';
import FavoritesEmptyState from './favorites_empty_state';
import SpaceItem from './space_item';
import SpacesCategory from './spaces_category';
import styles from './spaces_sidebar.module.scss';
import SpacesSidebarHeader from './spaces_sidebar_header';
import SpacesSidebarNav, {type DocsNavKey} from './spaces_sidebar_nav';
import SpacesSidebarSearch from './spaces_sidebar_search';
import {useSidebarSpaces} from './use_sidebar_spaces';

type Props = {
    onOpenSwitcher: () => void;
    onCreateSpace: () => void;
};

const SpacesSidebar = ({onOpenSwitcher, onCreateSpace}: Props) => {
    const {formatMessage} = useIntl();
    const {displayName: teamName, description: teamDescription} = useTeamContext();
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
            className={styles.root}
            aria-label={formatMessage({id: 'docs.sidebar.label', defaultMessage: 'Spaces'})}
        >
            <div className={styles.headerNav}>
                <SpacesSidebarHeader
                    teamName={teamName}
                    teamDescription={teamDescription}
                    onCreateSpace={onCreateSpace}
                    onBrowseSpaces={onOpenSwitcher}
                />
                <SpacesSidebarSearch onOpen={onOpenSwitcher}/>
            </div>
            <SpacesSidebarNav
                active={selectedSpaceId ? null : activeNav}
                onSelect={selectNav}
            />
            <div className={styles.scroll}>
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
                    <CreateSpaceButton onClick={onCreateSpace}/>
                </SpacesCategory>
            </div>
        </nav>
    );
};

export default SpacesSidebar;
