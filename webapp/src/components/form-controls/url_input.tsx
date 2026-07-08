// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import styles from './url_input.module.scss';

type Props = {
    id: string;
    baseUrl: string;
    value: string;
    onChange: (value: string) => void;
    onBlur?: () => void;
    error?: string;
};

const UrlInput = ({id, baseUrl, value, onChange, onBlur, error}: Props) => {
    const {formatMessage} = useIntl();
    const [editing, setEditing] = useState(false);

    const label = formatMessage({id: 'docs.form.url.label', defaultMessage: 'URL:'});

    const stopEditing = () => {
        setEditing(false);
        onBlur?.();
    };

    return (
        <div className={classNames(styles.root, {[styles.hasError]: Boolean(error)})}>
            {editing ? (
                <div className={styles.field}>
                    {/* eslint-disable-next-line formatjs/no-literal-string-in-jsx -- URL fragment, not translatable */}
                    <span className={styles.prefix}>{`${baseUrl}/`}</span>
                    <input
                        id={id}
                        className={styles.input}
                        value={value}
                        aria-label={formatMessage({id: 'docs.form.url.editLabel', defaultMessage: 'Space URL'})}
                        aria-invalid={Boolean(error)}
                        autoFocus={true}
                        onChange={(e) => onChange(e.target.value)}
                        onBlur={stopEditing}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter') {
                                e.preventDefault();
                                stopEditing();
                            }
                        }}
                    />
                </div>
            ) : (
                <div className={styles.previewRow}>
                    <span className={styles.preview}>
                        {/* eslint-disable-next-line formatjs/no-literal-string-in-jsx -- localized label + URL fragment */}
                        {`${label} ${baseUrl}/${value}`}
                    </span>
                    <button
                        type='button'
                        className={styles.edit}
                        onClick={() => setEditing(true)}
                    >
                        {formatMessage({id: 'docs.form.url.edit', defaultMessage: 'Edit'})}
                    </button>
                </div>
            )}
            {error && <div className={styles.error}>{error}</div>}
        </div>
    );
};

export default UrlInput;
