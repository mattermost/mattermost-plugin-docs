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

import DocsMainContent from './docs_main_content';
import styles from './docs_root.module.scss';

const DocsRoot = () => {
    useBootstrapDocs();

    const {spaceId, pageId, isDraft} = useDocsNavigation();
    const {id: teamId} = useTeamContext();

    const [switcherOpen, setSwitcherOpen] = useState(false);
    const openSwitcher = useCallback(() => setSwitcherOpen(true), []);
    const closeSwitcher = useCallback(() => setSwitcherOpen(false), []);

    const [createSpaceOpen, setCreateSpaceOpen] = useState(false);
    const openCreateSpace = useCallback(() => setCreateSpaceOpen(true), []);
    const closeCreateSpace = useCallback(() => setCreateSpaceOpen(false), []);

    // The import wizard is a panel rather than a modal: an import runs for minutes, and its owner should be able
    // to read the plan it asks them to approve without a dialog holding the rest of the product hostage. Closing
    // it stops nothing — the job is server-side work, and reopening finds it again.
    const [importOpen, setImportOpen] = useState(false);
    const openImport = useCallback(() => setImportOpen(true), []);
    const closeImport = useCallback(() => setImportOpen(false), []);

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
                {importOpen ? (
                    <ImportWizard
                        target={{kind: 'new', team_id: teamId}}
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
