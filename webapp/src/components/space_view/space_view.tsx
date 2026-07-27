// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceStats} from 'hooks/spaces';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import type {Space} from 'types/docs';

import PageBar from './page_bar';
import PageHero from './page_hero';
import SpaceTitleBar from './space_title_bar';
import styles from './space_view.module.scss';

// Main content for a routed space: the space title bar and page bar over the
// front-door page (hero). The page body is a placeholder until the editor is
// mounted (a later pass).
const SpaceView = ({space}: {space: Space}) => {
    const {pageCount, memberCount} = useSpaceStats(space.id);

    return (
        <div className={styles.root}>
            <SpaceTitleBar
                space={space}
                memberCount={memberCount}
            />
            <PageBar space={space}/>
            <div className={styles.scroll}>
                <div className={styles.content}>
                    <PageHero
                        space={space}
                        pageCount={pageCount}
                        memberCount={memberCount}
                    />
                    <div className={styles.body}>
                        <FormattedMessage
                            id='docs.space.bodyPlaceholder'
                            defaultMessage='Page content will appear here once the editor is available.'
                        />
                    </div>
                </div>
            </div>
        </div>
    );
};

export default SpaceView;
