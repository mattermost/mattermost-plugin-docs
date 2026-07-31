// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {AutosaveStatus} from 'hooks/draft_autosave';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import styles from './autosave_indicator.module.scss';

type Props = {
    status: AutosaveStatus;
};

const AutosaveIndicator = ({status}: Props) => (
    <span
        className={styles.root}
        role='status'
        aria-live='polite'
        data-status={status}
    >
        {status === 'saving' && (
            <FormattedMessage
                id='docs.editor.autosave.saving'
                defaultMessage='Saving...'
            />
        )}
        {status === 'saved' && (
            <FormattedMessage
                id='docs.editor.autosave.saved'
                defaultMessage='Saved'
            />
        )}
        {status === 'unsaved' && (
            <FormattedMessage
                id='docs.editor.autosave.unsaved'
                defaultMessage='Unsaved changes'
            />
        )}
    </span>
);

export default AutosaveIndicator;
