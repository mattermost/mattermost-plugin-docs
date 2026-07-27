// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useBootstrapDocs} from 'hooks/bootstrap';
import {useDocsNavigation} from 'hooks/navigation';
import React, {useCallback, useState} from 'react';
import {useHotkeys} from 'react-hotkeys-hook';

import CreateSpaceModal from 'components/create_space_modal/create_space_modal';
import DocsSwitcher from 'components/docs_switcher/docs_switcher';
import SpacesSidebar from 'components/spaces_sidebar/spaces_sidebar';

import DocsMainContent from './docs_main_content';
import styles from './docs_root.module.scss';

const DocsRoot = () => {
    useBootstrapDocs();

    const {spaceId, pageId} = useDocsNavigation();

    const [switcherOpen, setSwitcherOpen] = useState(false);
    const openSwitcher = useCallback(() => setSwitcherOpen(true), []);
    const closeSwitcher = useCallback(() => setSwitcherOpen(false), []);

    const [createSpaceOpen, setCreateSpaceOpen] = useState(false);
    const openCreateSpace = useCallback(() => setCreateSpaceOpen(true), []);
    const closeCreateSpace = useCallback(() => setCreateSpaceOpen(false), []);

    // stopPropagation so the Docs switcher wins the shortcut over the host's.
    useHotkeys('mod+k', (e) => {
        e.stopPropagation();
        setSwitcherOpen((open) => !open);
    }, {preventDefault: true, enableOnFormTags: true});

    return (
        <div className={styles.root}>
            <div className={styles.sidebar}>
                <SpacesSidebar
                    onOpenSwitcher={openSwitcher}
                    onCreateSpace={openCreateSpace}
                />
            </div>
            <main className={styles.main}>
                <DocsMainContent
                    spaceId={spaceId}
                    pageId={pageId}
                    onCreateSpace={openCreateSpace}
                    onBrowseSpaces={openSwitcher}
                />
            </main>
            {switcherOpen && <DocsSwitcher onClose={closeSwitcher}/>}
            {createSpaceOpen && <CreateSpaceModal onClose={closeCreateSpace}/>}
        </div>
    );
};

export default DocsRoot;
