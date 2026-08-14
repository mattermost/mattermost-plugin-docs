// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import styles from './text_input.module.scss';

type Props = {
    id: string;
    label: string;
    value: string;
    onChange: (value: string) => void;
    leading?: React.ReactNode;
    error?: string;
    maxLength?: number;
    autoFocus?: boolean;
    onEnter?: () => void;
};

const TextInput = ({id, label, value, onChange, leading, error, maxLength, autoFocus, onEnter}: Props) => {
    const errorId = error ? `${id}-error` : undefined;

    return (
        <div className={classNames(styles.root, {[styles.hasError]: Boolean(error)})}>
            <div className={styles.field}>
                {leading && <div className={styles.leading}>{leading}</div>}
                <input
                    id={id}
                    className={styles.input}

                    // A non-empty placeholder lets :placeholder-shown drive the
                    // label float without extra JS.
                    // eslint-disable-next-line formatjs/no-literal-string-in-jsx -- non-visible layout placeholder, not translatable
                    placeholder=' '
                    value={value}
                    maxLength={maxLength}
                    autoFocus={autoFocus}
                    aria-invalid={Boolean(error)}
                    aria-describedby={errorId}
                    onChange={(e) => onChange(e.target.value)}
                    onKeyDown={(e) => {
                        const composing = e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229;
                        if (e.key === 'Enter' && onEnter && !composing) {
                            e.preventDefault();
                            onEnter();
                        }
                    }}
                />
                <label
                    className={styles.label}
                    htmlFor={id}
                >
                    {label}
                </label>
            </div>
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

export default TextInput;
