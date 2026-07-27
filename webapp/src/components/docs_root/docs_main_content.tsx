// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpace} from 'hooks/spaces';
import React from 'react';
import {FormattedMessage} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';

import DocsHome from 'components/docs_home/docs_home';
import PageEditor from 'components/page_editor/page_editor';

import styles from './docs_main_content.module.scss';

type Props = {
    spaceId?: string;
    pageId?: string;
    isDraft?: boolean;
    onCreateSpace: () => void;
    onBrowseSpaces: () => void;
};

// The space view is built later; for now a routed space renders a placeholder
// that reflects the routed space/page. When a page is routed we hand off to
// PageEditor
const DocsMainContent = ({spaceId, pageId, isDraft, onCreateSpace, onBrowseSpaces}: Props) => {
    const space = useSpace(spaceId);

    if (!space) {
        return (
            <DocsHome
                onCreateSpace={onCreateSpace}
                onBrowseSpaces={onBrowseSpaces}
            />
        );
    }

    if (pageId) {
        return (
            <PageEditor
                spaceId={space.id}
                pageId={pageId}
                isDraft={Boolean(isDraft)}
            />
        );
    }

    return (
        <div className={styles.root}>
            <div className={styles.empty}>
                <h2 className={styles.title}>
                    {/* eslint-disable-next-line formatjs/no-literal-string-in-jsx -- decorative emoji, not translatable */}
                    <span
                        aria-hidden={true}
                        style={{marginInlineEnd: '0.25em'}}
                    >
                        <SpaceIcon
                            space={space}
                            size={20}
                        />
                    </span>
                    {space.title}
                </h2>
                <p className={styles.subtitle}>
                    <FormattedMessage
                        id='docs.main.spaceOverview'
                        defaultMessage='Space overview'
                    />
                </p>
            </div>
        </div>
    );
};

export default DocsMainContent;
