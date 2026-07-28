// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useTeamContext} from 'hooks/team';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import CreateSpaceButton from './create_space_button';
import type {DndCategory} from './dnd/types';
import SpaceItem from './space_item';
import SpacesCategory from './spaces_category';
import styles from './spaces_sidebar.module.scss';
import SpacesSidebarHeader from './spaces_sidebar_header';
import SpacesSidebarNav, {type DocsNavKey} from './spaces_sidebar_nav';
import SpacesSidebarSearch from './spaces_sidebar_search';
import {useSidebarSpaces} from './use_sidebar_spaces';

// Drag-to-reorder is fully built but gated off until sidebar ordering persists
// to user preferences (spec §4, Phase B). It writes only to component state
// today, so a reorder is lost when the user navigates away from Docs and back —
// shipping it non-persistent reads as broken rather than unfinished. Flip to
// true once persistence lands. (Favorites, same phase, are commented out below.)
const DND_ENABLED = false;

type Props = {
    onOpenSwitcher: () => void;
    onCreateSpace: () => void;
};

const SpacesSidebar = ({onOpenSwitcher, onCreateSpace}: Props) => {
    const {formatMessage} = useIntl();
    const {displayName: teamName, description: teamDescription} = useTeamContext();
    const {spaceId: selectedSpaceId, goToSpace, goHome} = useDocsNavigation();
    const {spacesById, spacesOrder} = useSidebarSpaces(DND_ENABLED);
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
                dndEnabled={DND_ENABLED}
                onSelect={goToSpace}
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
                {/*
                    Favorites category is deferred until ordering/favorites persist to
                    user preferences (spec §4, Phase B). The model (useSidebarSpaces)
                    still tracks favoriteOrder for drag-to-reorder; restore this block
                    and the favorite menu item (space_item_menu) when persistence lands.

                    <SpacesCategory
                        title={formatMessage({id: 'docs.sidebar.category.favorites', defaultMessage: 'Favorites'})}
                        collapsible={true}
                        collapsed={favoritesCollapsed}
                        onToggle={toggleFavoritesCollapsed}
                    >
                        {favoriteOrder.length === 0 ? <FavoritesEmptyState/> : favoriteOrder.map((id) => renderSpace(id, 'favorites'))}
                    </SpacesCategory>
                */}
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
