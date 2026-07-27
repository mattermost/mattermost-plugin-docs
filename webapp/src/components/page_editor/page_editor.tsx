// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import {hostCanUseEditor, hostGetEditor} from 'webapp_globals';

import styles from './page_editor.module.scss';

type Props = {
    spaceId: string;
    pageId: string;
    isDraft: boolean;
};

// Placeholder mount for the WYSIWYG editor. This ticket only wires the
// component to the page route and proves the host slice resolves
const PageEditor = ({spaceId, pageId, isDraft}: Props) => {
    if (!hostCanUseEditor()) {
        return (
            <div className={styles.empty}>
                <FormattedMessage
                    id='docs.editor.hostMissing'
                    defaultMessage='This Mattermost build does not publish the Docs editor. Update the server to edit pages here.'
                />
            </div>
        );
    }

    const editor = hostGetEditor();
    const providerCount = editor?.providers ? Object.keys(editor.providers).length : 0;

    return (
        <div className={styles.root}>
            <div className={styles.header}>
                <span>
                    {isDraft ? (
                        <FormattedMessage
                            id='docs.editor.header.draft'
                            defaultMessage='Draft · {spaceId} / {pageId}'
                            values={{spaceId, pageId}}
                        />
                    ) : (
                        <FormattedMessage
                            id='docs.editor.header.published'
                            defaultMessage='Published · {spaceId} / {pageId}'
                            values={{spaceId, pageId}}
                        />
                    )}
                </span>
            </div>
            <div className={styles.stub}>
                <FormattedMessage
                    id='docs.editor.stub.body'
                    defaultMessage='Editor is available and will mount here. Suggestion providers exposed: {providerCount}.'
                    values={{providerCount}}
                />
            </div>
        </div>
    );
};

export default PageEditor;
