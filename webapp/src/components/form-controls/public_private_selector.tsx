// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import CheckCircleIcon from '@mattermost/compass-icons/components/check-circle';

import styles from './public_private_selector.module.scss';

export type SelectorOption = {
    value: string;
    icon: React.ReactNode;
    title: string;
    description: string;

    // A disabled option is shown but not selectable (e.g. private spaces before
    // space-level permissions exist).
    disabled?: boolean;
    disabledReason?: string;
};

type Props = {
    ariaLabel: string;
    options: SelectorOption[];
    value: string;
    onChange: (value: string) => void;
};

const PublicPrivateSelector = ({ariaLabel, options, value, onChange}: Props) => {
    return (
        <div
            className={styles.root}
            role='radiogroup'
            aria-label={ariaLabel}
        >
            {options.map((option) => {
                const selected = option.value === value;
                return (
                    <button
                        key={option.value}
                        type='button'
                        role='radio'
                        aria-checked={selected}
                        aria-disabled={option.disabled}
                        title={option.disabled ? option.disabledReason : undefined}
                        disabled={option.disabled}
                        className={classNames(styles.card, {
                            [styles.selected]: selected,
                            [styles.disabled]: option.disabled,
                        })}
                        onClick={() => !option.disabled && onChange(option.value)}
                    >
                        <span className={styles.icon}>{option.icon}</span>
                        <span className={styles.textFrame}>
                            <span className={styles.title}>{option.title}</span>
                            <span className={styles.description}>{option.description}</span>
                        </span>
                        {selected && (
                            <span className={styles.check}>
                                <CheckCircleIcon size={20}/>
                            </span>
                        )}
                    </button>
                );
            })}
        </div>
    );
};

export default PublicPrivateSelector;
