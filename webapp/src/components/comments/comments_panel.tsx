// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import RhsPanel from 'components/rhs/rhs_panel';

import styles from './comments_panel.module.scss';

// Placeholder: the panel opens, closes and resizes like the rest of the RHS, and
// carries its state in the URL, but has no comments in it yet. Here so the header's
// Comments control leads somewhere while threads are built out.
const CommentsPanel = ({onClose}: {onClose: () => void}) => {
    const {formatMessage} = useIntl();

    return (
        <RhsPanel
            name={formatMessage({id: 'docs.comments.title', defaultMessage: 'Comments'})}
            widthKey='comments'
            onClose={onClose}
        >
            <p className={styles.empty}>
                <FormattedMessage
                    id='docs.comments.placeholder'
                    defaultMessage='Comments on this page will appear here.'
                />
            </p>
        </RhsPanel>
    );
};

export default CommentsPanel;
