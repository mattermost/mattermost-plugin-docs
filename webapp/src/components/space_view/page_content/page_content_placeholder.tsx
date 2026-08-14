// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import styles from './page_content_placeholder.module.scss';

const BLOCKS = [3, 2, 3];

// Fills the content column while the editor is unavailable. Purely decorative —
// it becomes the content-loading skeleton once page bodies are fetched, so it
// carries no text for screen readers to announce.
const PageContentPlaceholder = () => (
    <div
        className={styles.placeholder}
        aria-hidden={true}
    >
        {BLOCKS.map((lines, blockIndex) => (
            <div
                key={blockIndex}
                className={styles.block}
            >
                <div className={styles.heading}/>
                {Array.from({length: lines}, (_, lineIndex) => (
                    <div
                        key={lineIndex}
                        className={classNames(styles.line, {[styles.lineShort]: lineIndex === lines - 1})}
                    />
                ))}
            </div>
        ))}
    </div>
);

export default PageContentPlaceholder;
