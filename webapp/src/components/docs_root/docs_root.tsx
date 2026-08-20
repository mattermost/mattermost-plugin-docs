// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useBootstrapDocs} from 'hooks/bootstrap';
import {useFullscreen} from 'hooks/fullscreen';
import {useDocsNavigation} from 'hooks/navigation';
import {useSidebarWidth} from 'hooks/sidebar_width';
import {useRecordSpaceView} from 'hooks/spaces';
import {useTeamContext} from 'hooks/team';
import React, {useCallback, useState} from 'react';
import {useHotkeys} from 'react-hotkeys-hook';
import {useIntl} from 'react-intl';

import CreateSpaceModal from 'components/create_space_modal/create_space_modal';
import DocsSwitcher from 'components/docs_switcher/docs_switcher';
import ImportWizard from 'components/import_wizard/import_wizard';
import {DocsModalController, openDocsModal} from 'components/modals';
import {Readout} from 'components/readout';
import ResizableDivider from 'components/resizable_divider/resizable_divider';
import SpacesSidebar from 'components/spaces_sidebar/spaces_sidebar';
import {DocsToaster} from 'components/toast';

import type {ImportTargetRequest} from 'types/imports';

import DocsMainContent from './docs_main_content';
import styles from './docs_root.module.scss';

const DEFAULT_SPACES_WIDTH = 264;
const MIN_SPACES_WIDTH = 220;
const MAX_SPACES_WIDTH = 420;

// The spaces list scrolls, so its scrollbar sits on the edge the resize handle
// straddles; this moves the handle clear of it.
const SPACES_SCROLLBAR_CLEARANCE = 6;

const DocsRoot = () => {
    useBootstrapDocs();

    const {formatMessage} = useIntl();
    const {spaceId, goToSpace, goHome, isImport, goToImport} = useDocsNavigation();
    const {id: teamId} = useTeamContext();
    const {width, setWidth, commitWidth} = useSidebarWidth('spaces', DEFAULT_SPACES_WIDTH, {
        minWidth: MIN_SPACES_WIDTH,
        maxWidth: MAX_SPACES_WIDTH,
    });

    // Fullscreen gives the page the window by dropping this column; the space header
    // grows a "Back to all spaces" control to stand in for it.
    const {fullscreen} = useFullscreen();

    useRecordSpaceView(spaceId);

    const [switcherOpen, setSwitcherOpen] = useState(false);
    const openSwitcher = useCallback(() => setSwitcherOpen(true), []);
    const closeSwitcher = useCallback(() => setSwitcherOpen(false), []);

    const openCreateSpace = useCallback(() => {
        openDocsModal((modal) => (
            <CreateSpaceModal
                onClose={modal.close}
                onCreated={(space) => goToSpace(space.id)}
            />
        ));
    }, [goToSpace]);

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
            {!fullscreen && (
                <div
                    className={styles.sidebar}
                    style={{width}}
                >
                    <SpacesSidebar
                        onOpenSwitcher={openSwitcher}
                        onCreateSpace={openCreateSpace}
                        onImportSpace={openImport}
                    />
                    <ResizableDivider
                        ariaLabel={formatMessage({id: 'docs.sidebar.resizeSpaces', defaultMessage: 'Resize spaces sidebar'})}
                        side='left'
                        scrollbarClearance={SPACES_SCROLLBAR_CLEARANCE}
                        width={width}
                        minWidth={MIN_SPACES_WIDTH}
                        maxWidth={MAX_SPACES_WIDTH}
                        defaultWidth={DEFAULT_SPACES_WIDTH}
                        onResize={setWidth}
                        onResizeEnd={commitWidth}
                    />
                </div>
            )}
            <main className={styles.main}>
                {isImport ? (
                    <ImportWizard
                        target={importTarget}
                        onClose={closeImport}
                    />
                ) : (
                    <DocsMainContent
                        onCreateSpace={openCreateSpace}
                        onBrowseSpaces={openSwitcher}
                    />
                )}
            </main>
            {switcherOpen && <DocsSwitcher onClose={closeSwitcher}/>}
            <DocsModalController/>
            <DocsToaster/>
            <Readout/>
        </div>
    );
};

export default DocsRoot;
