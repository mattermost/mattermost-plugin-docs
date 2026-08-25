// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import type {Permission} from 'types/permissions';
import {Permissions} from 'types/permissions';

import styles from './space_settings_modal.module.scss';

type Props = {

    // The permissions to offer, in display order.
    options: readonly Permission[];
    selected: Permission[];
    disabled?: boolean;
    busy?: boolean;
    onChange: (next: Permission[]) => void;

    // Distinguishes the checkbox ids of two sets rendered on the same screen
    // (the space default and one row per member).
    idPrefix: string;
    legend: React.ReactNode;
};

// usePermissionLabels resolves the permission vocabulary to its user-facing
// labels. Defined as a hook rather than a module constant so the strings are
// translated at render time under the active locale.
export const usePermissionLabels = (): Record<Permission, string> => {
    const {formatMessage} = useIntl();

    return {
        [Permissions.READ_PAGE]: formatMessage({id: 'docs.permission.read_page', defaultMessage: 'View pages'}),
        [Permissions.CREATE_PAGE]: formatMessage({id: 'docs.permission.create_page', defaultMessage: 'Create pages'}),
        [Permissions.COMMENT_PAGE]: formatMessage({id: 'docs.permission.comment_page', defaultMessage: 'Comment on pages'}),
        [Permissions.EDIT_PAGE]: formatMessage({id: 'docs.permission.edit_page', defaultMessage: 'Edit pages'}),
        [Permissions.DELETE_OWN_PAGE]: formatMessage({id: 'docs.permission.delete_own_page', defaultMessage: 'Delete own pages'}),

        // Never rendered as toggles — manage_space and delete_space are effective-set-only, not
        // grantable — but the map is exhaustive over the vocabulary so that adding a grantable
        // permission cannot compile without a label.
        [Permissions.MANAGE_SPACE]: formatMessage({id: 'docs.permission.manage_space', defaultMessage: 'Manage space'}),
        [Permissions.DELETE_SPACE]: formatMessage({id: 'docs.permission.delete_space', defaultMessage: 'Archive space'}),
        [Permissions.DELETE_PAGE]: formatMessage({id: 'docs.permission.delete_page', defaultMessage: 'Delete any page'}),
        [Permissions.ADMIN_SPACE]: formatMessage({id: 'docs.permission.admin_space', defaultMessage: 'Administer space'}),
    };
};

const PermissionToggles = ({options, selected, disabled, busy, onChange, idPrefix, legend}: Props) => {
    const labels = usePermissionLabels();

    const toggle = (permission: Permission) => {
        // Checked here as well as on the input: `disabled` also covers a save in
        // flight, and a click dispatched just before that re-render would otherwise
        // send a second set built from the pre-save selection.
        if (disabled) {
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
                            disabled={disabled}
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
