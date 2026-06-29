// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/use_docs_navigation';
import React, {useCallback, useEffect, useState} from 'react';

import DocsSwitcher from 'components/docs_switcher/docs_switcher';
import SpacesSidebar from 'components/spaces_sidebar/spaces_sidebar';

import DocsMainContent from './docs_main_content';
import './docs_root.scss';

// The product main component owns the entire /docs subtree. Routing/selection
// is read through useDocsNavigation; this component only holds local UI state
// (the switcher open state) and the host integration effects.
const DocsRoot = () => {
    const {spaceId, pageId} = useDocsNavigation();

    const [switcherOpen, setSwitcherOpen] = useState(false);
    const openSwitcher = useCallback(() => setSwitcherOpen(true), []);
    const closeSwitcher = useCallback(() => setSwitcherOpen(false), []);

    useEffect(() => {
        // Mirrors the host's channels layout so the global header inherits the
        // correct theming while the Docs product is active.
        const root = document.getElementById('root');
        root?.classList.add('channel-view');
        return () => root?.classList.remove('channel-view');
    }, []);

    useEffect(() => {
        // Cmd/Ctrl+K opens the Find-docs switcher. Capture-phase + preventDefault
        // so it takes precedence over the host's channel switcher while Docs is active.
        const onKeyDown = (e: KeyboardEvent) => {
            if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && (e.key === 'k' || e.key === 'K')) {
                e.preventDefault();
                e.stopPropagation();
                setSwitcherOpen((open) => !open);
            }
        };
        window.addEventListener('keydown', onKeyDown, true);
        return () => window.removeEventListener('keydown', onKeyDown, true);
    }, []);

    return (
        <div className='DocsRoot'>
            <div className='DocsRoot__sidebar'>
                <SpacesSidebar onOpenSwitcher={openSwitcher}/>
            </div>
            <DocsMainContent
                spaceId={spaceId}
                pageId={pageId}
            />
            {switcherOpen && <DocsSwitcher onClose={closeSwitcher}/>}
        </div>
    );
};

export default DocsRoot;
