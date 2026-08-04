// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useBootstrapDocs} from 'hooks/bootstrap';
import {useDocsNavigation} from 'hooks/navigation';
import {useSidebarWidth} from 'hooks/sidebar_width';
import {useRecordSpaceView} from 'hooks/spaces';
import React, {useCallback, useState} from 'react';
import {useHotkeys} from 'react-hotkeys-hook';
import {useIntl} from 'react-intl';

import CreateSpaceModal from 'components/create_space_modal/create_space_modal';
import DocsSwitcher from 'components/docs_switcher/docs_switcher';
import {DocsModalController, openDocsModal} from 'components/modals';
import {Readout} from 'components/readout';
import ResizableDivider from 'components/resizable_divider/resizable_divider';
import SpacesSidebar from 'components/spaces_sidebar/spaces_sidebar';
import {DocsToaster} from 'components/toast';

import DocsMainContent from './docs_main_content';
import styles from './docs_root.module.scss';

const DEFAULT_SPACES_WIDTH = 264;
const MIN_SPACES_WIDTH = 220;
const MAX_SPACES_WIDTH = 420;

const DocsRoot = () => {
    useBootstrapDocs();

    const {formatMessage} = useIntl();
    const {spaceId} = useDocsNavigation();
    const {width, setWidth, commitWidth} = useSidebarWidth('spaces', DEFAULT_SPACES_WIDTH);

    useRecordSpaceView(spaceId);

    const [switcherOpen, setSwitcherOpen] = useState(false);
    const openSwitcher = useCallback(() => setSwitcherOpen(true), []);
    const closeSwitcher = useCallback(() => setSwitcherOpen(false), []);

    const openCreateSpace = useCallback(() => {
        openDocsModal((modal) => <CreateSpaceModal onClose={modal.close}/>);
    }, []);

    // stopPropagation so the Docs switcher wins the shortcut over the host's.
    useHotkeys('mod+k', (e) => {
        e.stopPropagation();
        setSwitcherOpen((open) => !open);
    }, {preventDefault: true, enableOnFormTags: true});

    return (
        <div className={styles.root}>
            <div
                className={styles.sidebar}
                style={{width}}
            >
                <SpacesSidebar
                    onOpenSwitcher={openSwitcher}
                    onCreateSpace={openCreateSpace}
                />
                <ResizableDivider
                    ariaLabel={formatMessage({id: 'docs.sidebar.resizeSpaces', defaultMessage: 'Resize spaces sidebar'})}
                    side='left'
                    width={width}
                    minWidth={MIN_SPACES_WIDTH}
                    maxWidth={MAX_SPACES_WIDTH}
                    defaultWidth={DEFAULT_SPACES_WIDTH}
                    onResize={setWidth}
                    onResizeEnd={commitWidth}
                />
            </div>
            <main className={styles.main}>
                <DocsMainContent
                    onCreateSpace={openCreateSpace}
                    onBrowseSpaces={openSwitcher}
                />
            </main>
            {switcherOpen && <DocsSwitcher onClose={closeSwitcher}/>}
            <DocsModalController/>
            <DocsToaster/>
            <Readout/>
        </div>
    );
};

export default DocsRoot;
