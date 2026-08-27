// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {usePermissionLabels} from 'hooks/permission_labels';
import React from 'react';

import type {Permission} from 'types/permissions';

import styles from './space_settings_modal.module.scss';

type Props = {

    // The permissions to offer, in display order.
    options: readonly Permission[];
    selected: Permission[];
    disabled?: boolean;
    disabledOptions?: readonly Permission[];
    busy?: boolean;
    onChange: (next: Permission[]) => void;

    // Distinguishes the checkbox ids of two sets rendered on the same screen
    // (the space default and one row per member).
    idPrefix: string;
    legend: React.ReactNode;
};

const PermissionToggles = ({options, selected, disabled, disabledOptions = [], busy, onChange, idPrefix, legend}: Props) => {
    const labels = usePermissionLabels();

    const toggle = (permission: Permission) => {
        // Checked here as well as on the input: `disabled` also covers a save in
        // flight, and a click dispatched just before that re-render would otherwise
        // send a second set built from the pre-save selection.
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
            {options.map((permission) => {
                const id = `${idPrefix}-${permission}`;
                return (
                    <div
                        key={permission}
                        className={styles.toggle}
                    >
                        <input
                            id={id}
                            type='checkbox'
                            checked={selected.includes(permission)}
                            disabled={disabled || disabledOptions.includes(permission)}
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
