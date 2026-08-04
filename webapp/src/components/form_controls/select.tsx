// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Select as BaseSelect} from '@base-ui-components/react/select';
import classNames from 'classnames';
import React from 'react';

import CheckIcon from '@mattermost/compass-icons/components/check';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';

import styles from './select.module.scss';

export type SelectOption = {
    value: string;
    label: string;
    leadingIcon?: React.ReactNode;
};

type Props = {
    id: string;

    /** Floated label above the field, matching TextInput. */
    label: string;
    value: string;
    options: SelectOption[];
    onChange: (value: string) => void;
    disabled?: boolean;
};

/**
 * A single-select field with the same floated-label chrome as TextInput. Built
 * on Base UI's Select, so keyboard navigation, typeahead and the listbox
 * semantics come from the primitive.
 */
const Select = ({id, label, value, options, onChange, disabled}: Props) => {
    const selected = options.find((option) => option.value === value);

    return (
        <BaseSelect.Root
            value={value}
            disabled={disabled}
            onValueChange={(next) => onChange(next == null ? '' : String(next))}
        >
            <BaseSelect.Trigger
                id={id}
                className={styles.trigger}
            >
                <span className={styles.label}>{label}</span>
                {selected?.leadingIcon && (
                    <span
                        className={styles.leadingIcon}
                        aria-hidden={true}
                    >
                        {selected.leadingIcon}
                    </span>
                )}
                <span className={styles.value}>
                    <BaseSelect.Value>{selected?.label ?? ''}</BaseSelect.Value>
                </span>
                <BaseSelect.Icon className={styles.chevron}>
                    <ChevronDownIcon size={16}/>
                </BaseSelect.Icon>
            </BaseSelect.Trigger>
            <BaseSelect.Portal>
                {/* Viewport coordinates, not document. Base UI defaults to
                    `absolute`, which places the popup in the document while the
                    trigger can sit inside a `position: fixed` container — a modal —
                    so the two drift apart the moment anything scrolls. `fixed`
                    matches the coordinate space the trigger is actually in, and is
                    equally correct outside a modal. */}
                <BaseSelect.Positioner
                    className={styles.positioner}
                    positionMethod='fixed'
                    side='bottom'
                    align='start'
                    sideOffset={4}
                    collisionPadding={8}
                >
                    <BaseSelect.Popup className={styles.popup}>
                        <BaseSelect.List>
                            {options.map((option) => (
                                <BaseSelect.Item
                                    key={option.value}
                                    value={option.value}
                                    className={styles.item}
                                >
                                    {option.leadingIcon && (
                                        <span
                                            className={styles.leadingIcon}
                                            aria-hidden={true}
                                        >
                                            {option.leadingIcon}
                                        </span>
                                    )}
                                    <BaseSelect.ItemText className={classNames(styles.value, styles.itemText)}>
                                        {option.label}
                                    </BaseSelect.ItemText>
                                    <BaseSelect.ItemIndicator className={styles.indicator}>
                                        <CheckIcon size={16}/>
                                    </BaseSelect.ItemIndicator>
                                </BaseSelect.Item>
                            ))}
                        </BaseSelect.List>
                    </BaseSelect.Popup>
                </BaseSelect.Positioner>
            </BaseSelect.Portal>
        </BaseSelect.Root>
    );
};

export default Select;
