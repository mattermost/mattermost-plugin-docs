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
                {/* alignItemWithTrigger is Base UI's default and is why this popup
                    would not stay with its trigger. In that mode the popup overlaps
                    the trigger so the selected item's text lines up with the trigger's
                    value — macOS-style — and to do it Base UI gives the positioner a
                    static style, sets `disableAnchorTracking`, reports side 'none' and
                    locks scrolling (SelectPositioner lines 96, 110, 114-115). floating-
                    ui's computed position is not used at all, side/align/sideOffset are
                    ignored, and nothing follows the trigger — so inside a scrollable
                    modal pane the popup sits where it first landed while the field
                    scrolls away from it.

                    Off, it is an ordinary anchored popup: tracking on, our side and
                    offsets honoured, and positionMethod below actually applied.

                    Viewport coordinates rather than document: the trigger can sit in a
                    modal, which is `position: fixed`, and matching that space keeps the
                    two from drifting apart on scroll. Equally correct outside a modal. */}
                <BaseSelect.Positioner
                    className={styles.positioner}
                    alignItemWithTrigger={false}
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
