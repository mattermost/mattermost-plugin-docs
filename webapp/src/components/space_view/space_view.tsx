// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useSpaceStats} from 'hooks/spaces';
import React, {useCallback, useState} from 'react';
import {FormattedMessage} from 'react-intl';

import SpaceInfoPanel from 'components/space_info/space_info_panel';
import SpaceSettingsModal from 'components/space_settings_modal/space_settings_modal';

import type {Space} from 'types/docs';

import PageHeader from './page_header';
import PageHero from './page_hero';
import PageTreePanel from './page_tree/page_tree_panel';
import SpaceHeader from './space_header';
import styles from './space_view.module.scss';

// Main content for a routed space: the space header and page header stack
// full-width at the top; below them a flex row holds the pages sidebar (which
// slides in/out), the page content column (front-door hero over the editor
// area), and the optional space info panel as a right column. The page body is
// a placeholder until the editor is mounted.
const SpaceView = ({space}: {space: Space}) => {
    const {pageCount, memberCount} = useSpaceStats(space.id);
    const [treeOpen, setTreeOpen] = useState(true);
    const [infoOpen, setInfoOpen] = useState(false);
    const [settingsOpen, setSettingsOpen] = useState(false);

    const togglePages = useCallback(() => setTreeOpen((open) => !open), []);
    const toggleInfo = useCallback(() => setInfoOpen((open) => !open), []);
    const openSettings = useCallback(() => setSettingsOpen(true), []);

    return (
        <div className={styles.root}>
            <SpaceHeader
                space={space}
                memberCount={memberCount}
                infoOpen={infoOpen}
                onToggleInfo={toggleInfo}
                onOpenSettings={openSettings}
            />
            <PageHeader
                space={space}
                treeOpen={treeOpen}
                onTogglePages={togglePages}
            />
            <div className={styles.body}>
                <div className={classNames(styles.sidebar, {[styles.sidebarOpen]: treeOpen})}>
                    <PageTreePanel space={space}/>
                </div>
                <div className={styles.main}>
                    <div className={styles.scroll}>
                        <div className={styles.content}>
                            <PageHero
                                space={space}
                                pageCount={pageCount}
                                memberCount={memberCount}
                            />
                            <div className={styles.placeholder}>
                                <FormattedMessage
                                    id='docs.space.bodyPlaceholder'
                                    defaultMessage='Page content will appear here once the editor is available.'
                                />
                            </div>
                        </div>
                    </div>
                </div>
                {infoOpen && (
                    <SpaceInfoPanel
                        space={space}
                        onClose={toggleInfo}
                    />
                )}
            </div>
            {settingsOpen && (
                <SpaceSettingsModal
                    space={space}
                    onClose={() => setSettingsOpen(false)}
                />
            )}
        </div>
    );
};

export default SpaceView;
