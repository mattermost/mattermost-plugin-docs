// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceStats} from 'hooks/spaces';
import React, {useCallback, useState} from 'react';
import {FormattedMessage} from 'react-intl';

import type {Space} from 'types/docs';

import PageBar from './page_bar';
import PageHero from './page_hero';
import PageTreePanel from './page_tree/page_tree_panel';
import SpaceTitleBar from './space_title_bar';
import styles from './space_view.module.scss';

// Main content for a routed space: the space title bar spans the full width; a
// flex row holds the page tree panel and the content column (page bar over the
// front-door hero). The page body is a placeholder until the editor is mounted.
const SpaceView = ({space}: {space: Space}) => {
    const {pageCount, memberCount} = useSpaceStats(space.id);
    const [treeOpen, setTreeOpen] = useState(true);

    const togglePages = useCallback(() => setTreeOpen((open) => !open), []);

    return (
        <div className={styles.root}>
            <SpaceTitleBar
                space={space}
                memberCount={memberCount}
            />
            <div className={styles.body}>
                {treeOpen && <PageTreePanel space={space}/>}
                <div className={styles.main}>
                    <PageBar
                        space={space}
                        treeOpen={treeOpen}
                        onTogglePages={togglePages}
                    />
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
            </div>
        </div>
    );
};

export default SpaceView;
