// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpace} from 'hooks/spaces';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import DocsHome from 'components/docs_home/docs_home';

import styles from './docs_main_content.module.scss';

type Props = {
    spaceId?: string;
    pageId?: string;
    onCreateSpace: () => void;
    onBrowseSpaces: () => void;
};

// The space view is built later; for now a routed space renders a placeholder
// that reflects the routed space/page to keep the URL observable.
const DocsMainContent = ({spaceId, pageId, onCreateSpace, onBrowseSpaces}: Props) => {
    const space = useSpace(spaceId);

    if (!space) {
        return (
            <DocsHome
                onCreateSpace={onCreateSpace}
                onBrowseSpaces={onBrowseSpaces}
            />
        );
    }

    return (
        <div className={styles.root}>
            <div className={styles.empty}>
                <h2 className={styles.title}>
                    {/* eslint-disable-next-line formatjs/no-literal-string-in-jsx -- decorative emoji, not translatable */}
                    <span aria-hidden={true}>{space.icon ? `${space.icon} ` : null}</span>
                    {space.title}
                </h2>
                <p className={styles.subtitle}>
                    {pageId ? (
                        <FormattedMessage
                            id='docs.main.page'
                            defaultMessage='Page {pageId}'
                            values={{pageId}}
                        />
                    ) : (
                        <FormattedMessage
                            id='docs.main.spaceOverview'
                            defaultMessage='Space overview'
                        />
                    )}
                </p>
            </div>
        </div>
    );
};

export default DocsMainContent;
