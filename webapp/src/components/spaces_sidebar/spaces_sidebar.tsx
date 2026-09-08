// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useTeamContext} from 'hooks/team';
import React from 'react';
import {useIntl} from 'react-intl';

import CreateSpaceButton from './create_space_button';
import type {DndCategory} from './dnd/types';
import FavoritesEmptyState from './favorites_empty_state';
import SpaceItem from './space_item';
import SpacesCategory from './spaces_category';
import styles from './spaces_sidebar.module.scss';
import SpacesSidebarHeader from './spaces_sidebar_header';
import SpacesSidebarNav from './spaces_sidebar_nav';
import SpacesSidebarSearch from './spaces_sidebar_search';
import {useSidebarSpaces} from './use_sidebar_spaces';

// Drag-to-reorder and favorites both persist to user preferences now
// (data/favorites), so the ordering survives navigation and reloads.
const DND_ENABLED = true;

type Props = {
    onOpenSwitcher: () => void;
    onCreateSpace?: () => void;
};

const SpacesSidebar = ({onOpenSwitcher, onCreateSpace}: Props) => {
    const {formatMessage} = useIntl();
    const {displayName: teamName, description: teamDescription} = useTeamContext();
    const {spaceId: selectedSpaceId, paths} = useDocsNavigation();
    const {spacesById, spacesOrder, favoriteSpaceIds, favoritesCollapsed, toggleFavoritesCollapsed} = useSidebarSpaces(DND_ENABLED);

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
                href={paths.space(space.id)}
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

                // Home is active whenever no space is routed — derived from the URL
                // rather than tracked, so a back/forward navigation can't desync it.
                active={selectedSpaceId ? null : 'home'}
                homeHref={paths.home()}
            />
            <div className={styles.scroll}>
                <SpacesCategory
                    title={formatMessage({id: 'docs.sidebar.category.favorites', defaultMessage: 'Favorites'})}
                    collapsible={true}
                    collapsed={favoritesCollapsed}
                    onToggle={toggleFavoritesCollapsed}
                >
                    {favoriteSpaceIds.length === 0 ? <FavoritesEmptyState/> : favoriteSpaceIds.map((id) => renderSpace(id, 'favorites'))}
                </SpacesCategory>
                <SpacesCategory
                    title={formatMessage({id: 'docs.sidebar.category.spaces', defaultMessage: 'Spaces'})}
                    collapsible={false}
                >
                    {spacesOrder.map((id) => renderSpace(id, 'spaces'))}
                    {onCreateSpace && <CreateSpaceButton onClick={onCreateSpace}/>}
                </SpacesCategory>
            </div>
        </nav>
    );
};

export default SpacesSidebar;
