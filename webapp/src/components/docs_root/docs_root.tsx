// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useBootstrapDocs} from 'hooks/bootstrap';
import {useDocsNavigation} from 'hooks/navigation';
import {useTeamContext} from 'hooks/team';
import React, {useCallback, useState} from 'react';
import {useHotkeys} from 'react-hotkeys-hook';

import CreateSpaceModal from 'components/create_space_modal/create_space_modal';
import DocsSwitcher from 'components/docs_switcher/docs_switcher';
import ImportWizard from 'components/import_wizard/import_wizard';
import SpacesSidebar from 'components/spaces_sidebar/spaces_sidebar';

import type {ImportTargetRequest} from 'types/imports';

import DocsMainContent from './docs_main_content';
import styles from './docs_root.module.scss';

const DocsRoot = () => {
    useBootstrapDocs();

    const {spaceId, pageId, isDraft, isImport, goToImport, goToSpace, goHome} = useDocsNavigation();
    const {id: teamId} = useTeamContext();

    const [switcherOpen, setSwitcherOpen] = useState(false);
    const openSwitcher = useCallback(() => setSwitcherOpen(true), []);
    const closeSwitcher = useCallback(() => setSwitcherOpen(false), []);

    const [createSpaceOpen, setCreateSpaceOpen] = useState(false);
    const openCreateSpace = useCallback(() => setCreateSpaceOpen(true), []);
    const closeCreateSpace = useCallback(() => setCreateSpaceOpen(false), []);

    // The import wizard is a routed panel rather than a modal, for two reasons that are really one. An import
    // runs for minutes and its owner has to read a plan before approving it, which a dialog holding the product
    // hostage makes worse; and because the job outlives any view of it, where you are in an import belongs in the
    // URL — so a reload, a link, or coming back tomorrow all arrive at the same place. Leaving stops nothing.
    const openImport = useCallback(() => goToImport(), [goToImport]);
    const closeImport = useCallback((importedSpaceId?: string) => {
        // Back to whatever the import was about: the Space it just filled if it finished, the Space it was
        // importing into, or the product home when there is no Space to show yet.
        const destination = importedSpaceId ?? spaceId;
        if (destination) {
            goToSpace(destination);
            return;
        }
        goHome();
    }, [spaceId, goToSpace, goHome]);

    // A Space in the URL means an import into that Space; without one, the import creates a Space.
    const importTarget: ImportTargetRequest = spaceId ? {kind: 'existing', space_id: spaceId} : {kind: 'new', team_id: teamId};

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
                    onImportSpace={openImport}
                />
            </div>
            <main className={styles.main}>
                {isImport ? (
                    <ImportWizard
                        target={importTarget}
                        onClose={closeImport}
                    />
                ) : (
                    <DocsMainContent
                        spaceId={spaceId}
                        pageId={pageId}
                        isDraft={isDraft}
                        onCreateSpace={openCreateSpace}
                        onBrowseSpaces={openSwitcher}
                    />
                )}
            </main>
            {switcherOpen && <DocsSwitcher onClose={closeSwitcher}/>}
            {createSpaceOpen && <CreateSpaceModal onClose={closeCreateSpace}/>}
        </div>
    );
};

export default DocsRoot;
