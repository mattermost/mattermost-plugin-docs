// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import styles from './text_area.module.scss';

type Props = {
    id: string;

    // Used as both the placeholder and the accessible name.
    label: string;
    value: string;
    onChange: (value: string) => void;
    error?: string;
    maxLength?: number;
    rows?: number;
    autoFocus?: boolean;
};

const TextArea = ({id, label, value, onChange, error, maxLength, rows = 3, autoFocus}: Props) => {
    const errorId = error ? `${id}-error` : undefined;

    return (
        <div className={classNames(styles.root, {[styles.hasError]: Boolean(error)})}>
            <textarea
                id={id}
                className={styles.field}
                placeholder={label}
                aria-label={label}
                aria-invalid={Boolean(error)}
                aria-describedby={errorId}
                value={value}
                rows={rows}
                maxLength={maxLength}
                autoFocus={autoFocus}
                onChange={(e) => onChange(e.target.value)}
            />
            {error && (
                <div
                    id={errorId}
                    className={styles.error}
                >
                    {error}
                </div>
            )}
        </div>
    );
};

export default TextArea;
