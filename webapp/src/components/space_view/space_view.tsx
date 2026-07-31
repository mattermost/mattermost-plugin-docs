// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useDefaultPagePath} from 'hooks/pages';
import {useAppSelector} from 'hooks/redux';
import {useSpaceStats} from 'hooks/spaces';
import React, {useCallback, useState} from 'react';
import {Redirect} from 'react-router-dom';

import {getPage} from 'store/selectors';

import SpaceInfoPanel from 'components/space_info/space_info_panel';
import type {SpaceInfoView} from 'components/space_info/space_info_panel';

import type {Space} from 'types/docs';

import PageContent from './page_content/page_content';
import PageContentPlaceholder from './page_content/page_content_placeholder';
import PageHeader from './page_header';
import PageHero from './page_hero';
import PageTreePanel from './page_tree/page_tree_panel';
import Sidebar from './sidebar/sidebar';
import SpaceHeader from './space_header';
import styles from './space_view.module.scss';

// Main content for a routed space. The space info panel is a full-height right
// column, so its own header lines up with the space header rather than starting
// below it; everything else lives in the primary column to its left — the space
// header and page header stacked at the top, then a row of the resizable pages
// sidebar and the page content. The content column shows the space front door
// (hero) until a page is routed, at which point it shows that page instead.
// Page bodies are a placeholder until the editor is mounted.
const SpaceView = ({space}: {space: Space}) => {
    const {pageId} = useDocsNavigation();
    const page = useAppSelector((state) => (pageId ? getPage(state, pageId) : undefined));
    const {pageCount, memberCount} = useSpaceStats(space.id);
    const defaultPagePath = useDefaultPagePath(space);
    const [treeOpen, setTreeOpen] = useState(true);
    const [infoView, setInfoView] = useState<SpaceInfoView | null>(null);

    const togglePages = useCallback(() => setTreeOpen((open) => !open), []);
    const toggleInfo = useCallback(() => setInfoView((view) => (view ? null : 'root')), []);
    const closeInfo = useCallback(() => setInfoView(null), []);
    const showMembers = useCallback(() => setInfoView('members'), []);

    // A space with a default page opens on that page; `<Redirect>` replaces the
    // space-home entry rather than pushing one.
    if (defaultPagePath) {
        return <Redirect to={defaultPagePath}/>;
    }

    return (
        <div className={styles.root}>
            <div className={styles.primary}>
                <SpaceHeader
                    space={space}
                    memberCount={memberCount}
                    infoOpen={infoView !== null}
                    onToggleInfo={toggleInfo}
                    onShowMembers={showMembers}
                />
                <PageHeader
                    space={space}
                    page={page}
                    treeOpen={treeOpen}
                    onTogglePages={togglePages}
                />
                <div className={styles.body}>
                    <Sidebar open={treeOpen}>
                        <PageTreePanel space={space}/>
                    </Sidebar>
                    <div className={styles.main}>
                        <div className={styles.scroll}>
                            {pageId ? (
                                <PageContent pageId={pageId}/>
                            ) : (
                                <div className={styles.content}>
                                    <PageHero
                                        space={space}
                                        pageCount={pageCount}
                                        memberCount={memberCount}
                                    />
                                    <PageContentPlaceholder/>
                                </div>
                            )}
                        </div>
                    </div>
                </div>
            </div>
            {infoView && (
                <SpaceInfoPanel
                    space={space}
                    view={infoView}
                    onViewChange={setInfoView}
                    onClose={closeInfo}
                />
            )}
        </div>
    );
};

export default SpaceView;
