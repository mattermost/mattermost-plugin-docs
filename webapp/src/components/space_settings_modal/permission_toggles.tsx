// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {usePermissionLabels} from 'hooks/permission_labels';
import React from 'react';

import type {Permission} from 'types/permissions';

import styles from './space_settings_modal.module.scss';

type Props = {

    // The permissions to offer, in display order. May be empty when the caller renders only
    // the header under the legend.
    options: readonly Permission[];
    selected: Permission[];
    disabled?: boolean;
    disabledOptions?: readonly Permission[];

    /** Shown as `title` on an input disabled by `disabled`. */
    disabledReason?: string;

    /** Shown as `title` on an input disabled individually via `disabledOptions`. */
    disabledOptionsReason?: string;
    busy?: boolean;
    onChange: (next: Permission[]) => void;

    // Distinguishes the checkbox ids of two sets rendered on the same screen
    // (the space default and one row per member).
    idPrefix: string;
    legend: React.ReactNode;

    // Rendered between the legend and the checkboxes, inside the same group: the named tiers
    // that the checkboxes refine.
    header?: React.ReactNode;
};

const PermissionToggles = ({options, selected, disabled, disabledOptions = [], disabledReason, disabledOptionsReason, busy, onChange, idPrefix, legend, header}: Props) => {
    const labels = usePermissionLabels();

    const toggle = (permission: Permission) => {
        // Checked here as well as on the input, since the input's `disabled` re-renders
        // after a save starts; this keeps a click in that window from sending a second
        // set built from the pre-save selection.
        if (disabled || disabledOptions.includes(permission)) {
            return;
        }

        // Rebuilt from options rather than by pushing onto selected, so the sent
        // set keeps a stable order and cannot accumulate duplicates.
        const next = options.filter((option) => (
            option === permission ? !selected.includes(option) : selected.includes(option)
        ));
        onChange(next);
    };

    return (
        <fieldset
            className={styles.toggles}
            aria-busy={busy}
        >
            <legend className={styles.togglesLegend}>{legend}</legend>
            {header}
            {options.map((permission) => {
                const id = `${idPrefix}-${permission}`;
                const optionLocked = disabledOptions.includes(permission);
                let reason;
                if (disabled) {
                    reason = disabledReason;
                } else if (optionLocked) {
                    reason = disabledOptionsReason;
                }
                return (
                    <div
                        key={permission}
                        className={styles.toggle}
                    >
                        <input
                            id={id}
                            type='checkbox'
                            checked={selected.includes(permission)}
                            disabled={disabled || optionLocked}
                            title={reason}
                            onChange={() => toggle(permission)}
                        />
                        <label htmlFor={id}>{labels[permission]}</label>
                    </div>
                );
            })}
        </fieldset>
    );
};

export default PermissionToggles;
