// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {usePageDraft, usePublishDraft} from 'hooks/drafts';
import {useDocsNavigation, useTogglePageEditMode} from 'hooks/navigation';
import {useDefaultPagePath} from 'hooks/pages';
import {useAppSelector} from 'hooks/redux';
import {useRhs} from 'hooks/rhs';
import {useSpaceStats} from 'hooks/spaces';
import React, {useCallback, useState} from 'react';
import {Redirect} from 'react-router-dom';

import {arePagesLoadedForSpace, getPageInSpace} from 'store/selectors';

import CommentsPanel from 'components/comments/comments_panel';
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
    const {pageId, isDraft, isEditing, paths} = useDocsNavigation();
    const toggleEdit = useTogglePageEditMode(space.id);
    const page = useAppSelector((state) => (pageId ? getPageInSpace(state, space.id, pageId) : undefined));
    const pagesLoaded = useAppSelector((state) => arePagesLoadedForSpace(state, space.id));

    // Loaded for any routed page, not just the draft route: a published page can
    // have unpublished edits too, and that is what Update acts on.
    const {draft, loaded: draftLoaded} = usePageDraft(space.id, pageId);
    const publish = usePublishDraft(space.id);
    const onPublish = useCallback(() => (pageId ? publish(pageId) : undefined), [publish, pageId]);
    const {pageCount, memberCount} = useSpaceStats(space.id);
    const defaultPagePath = useDefaultPagePath(space);
    const [treeOpen, setTreeOpen] = useState(true);

    // Which panel sits in the right column, from the URL — see useRhs for why
    // opening one replaces the history entry rather than pushing it.
    const {rhs, openRhs, closeRhs, toggleRhs} = useRhs();

    const togglePages = useCallback(() => setTreeOpen((open) => !open), []);
    const toggleInfo = useCallback(() => toggleRhs('info'), [toggleRhs]);
    const toggleComments = useCallback(() => toggleRhs('comments'), [toggleRhs]);
    const showMembers = useCallback(() => openRhs('info', 'members'), [openRhs]);

    // 'root' is the panel's default screen, so it stays out of the URL.
    const showInfoView = useCallback((view: SpaceInfoView) => openRhs('info', view === 'root' ? undefined : view), [openRhs]);

    // A space with a default page opens on that page; `<Redirect>` replaces the
    // space-home entry rather than pushing one.
    if (defaultPagePath) {
        return <Redirect to={defaultPagePath}/>;
    }

    // A draft URL whose page now exists means the draft was published — here or in
    // another tab. The page is the canonical address for it, so hand over.
    if (isDraft && pageId && page) {
        return <Redirect to={paths.page(space.id, pageId)}/>;
    }

    // A draft URL naming no draft was discarded or never existed. Waiting for the
    // fetch to settle first matters: correcting it earlier would turn a slow
    // response into a bounce out of a draft that is really there.
    if (isDraft && draftLoaded && !draft) {
        return <Redirect to={paths.overview(space.id)}/>;
    }

    // Once the space's pages are loaded, a routed id that isn't among them names
    // a page in another space or one that's gone, so fall back to the front door
    // rather than rendering a page shell that can never fill in. /overview is
    // explicit, so it won't bounce back through the default-page redirect above.
    // Excludes the draft route, where having no page is the normal case.
    if (!isDraft && pageId && pagesLoaded && !page) {
        return <Redirect to={paths.overview(space.id)}/>;
    }

    return (
        <div className={styles.root}>
            <div className={styles.primary}>
                <SpaceHeader
                    space={space}
                    memberCount={memberCount}
                    infoOpen={rhs?.id === 'info'}
                    onToggleInfo={toggleInfo}
                    onShowMembers={showMembers}
                />
                <PageHeader
                    space={space}
                    page={page}
                    draft={draft}
                    treeOpen={treeOpen}
                    editing={isEditing}
                    commentsOpen={rhs?.id === 'comments'}
                    onTogglePages={togglePages}
                    onToggleComments={toggleComments}
                    onToggleEdit={toggleEdit}
                    onPublish={onPublish}
                />
                <div className={styles.body}>
                    <Sidebar open={treeOpen}>
                        <PageTreePanel space={space}/>
                    </Sidebar>
                    <div className={styles.main}>
                        <div className={styles.scroll}>
                            {pageId ? (
                                <PageContent
                                    page={page}
                                    draft={draft}
                                    editing={isEditing}
                                />
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
            {rhs?.id === 'info' && (
                <SpaceInfoPanel
                    space={space}
                    view={rhs.view === 'members' ? 'members' : 'root'}
                    onViewChange={showInfoView}
                    onClose={closeRhs}
                />
            )}
            {rhs?.id === 'comments' && <CommentsPanel onClose={closeRhs}/>}
        </div>
    );
};

export default SpaceView;
