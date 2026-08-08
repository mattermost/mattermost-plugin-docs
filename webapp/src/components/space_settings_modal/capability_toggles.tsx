// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import type {Capability} from 'types/permissions';
import {Capabilities} from 'types/permissions';

import styles from './space_settings_modal.module.scss';

type Props = {

    // The capabilities to offer, in display order.
    options: Capability[];
    selected: Capability[];
    disabled?: boolean;
    busy?: boolean;
    onChange: (next: Capability[]) => void;

    // Distinguishes the checkbox ids of two sets rendered on the same screen
    // (the space default and one row per member).
    idPrefix: string;
    legend: React.ReactNode;
};

// useCapabilityLabels resolves the capability vocabulary to its user-facing
// labels. Defined as a hook rather than a module constant so the strings are
// translated at render time under the active locale.
export const useCapabilityLabels = (): Record<Capability, string> => {
    const {formatMessage} = useIntl();

    return {
        [Capabilities.READ_PAGE]: formatMessage({id: 'docs.capability.read_page', defaultMessage: 'View pages'}),
        [Capabilities.CREATE_PAGE]: formatMessage({id: 'docs.capability.create_page', defaultMessage: 'Create pages'}),
        [Capabilities.COMMENT_PAGE]: formatMessage({id: 'docs.capability.comment_page', defaultMessage: 'Comment on pages'}),
        [Capabilities.EDIT_PAGE]: formatMessage({id: 'docs.capability.edit_page', defaultMessage: 'Edit pages'}),
        [Capabilities.DELETE_OWN_PAGE]: formatMessage({id: 'docs.capability.delete_own_page', defaultMessage: 'Delete own pages'}),
        [Capabilities.DELETE_PAGE]: formatMessage({id: 'docs.capability.delete_page', defaultMessage: 'Delete any page'}),
        [Capabilities.ADMIN_SPACE]: formatMessage({id: 'docs.capability.admin_space', defaultMessage: 'Administer space'}),
    };
};

const CapabilityToggles = ({options, selected, disabled, busy, onChange, idPrefix, legend}: Props) => {
    const labels = useCapabilityLabels();

    const toggle = (capability: Capability) => {
        // Rebuilt from options rather than by pushing onto selected, so the sent
        // set keeps a stable order and cannot accumulate duplicates.
        const next = options.filter((option) => (
            option === capability ? !selected.includes(option) : selected.includes(option)
        ));
        onChange(next);
    };

    return (
        <fieldset
            className={styles.toggles}
            aria-busy={busy}
        >
            <legend className={styles.togglesLegend}>{legend}</legend>
            {options.map((capability) => {
                const id = `${idPrefix}-${capability}`;
                return (
                    <div
                        key={capability}
                        className={styles.toggle}
                    >
                        <input
                            id={id}
                            type='checkbox'
                            checked={selected.includes(capability)}
                            disabled={disabled}
                            onChange={() => toggle(capability)}
                        />
                        <label htmlFor={id}>{labels[capability]}</label>
                    </div>
                );
            })}
        </fieldset>
    );
};

export default CapabilityToggles;
