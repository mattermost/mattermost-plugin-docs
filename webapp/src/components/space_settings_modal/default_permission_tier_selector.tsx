// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {usePermissionTierLabels} from 'hooks/permission_labels';
import React from 'react';
import {FormattedMessage} from 'react-intl';
import {PERMISSION_TIERS, TIER_PERMISSIONS, samePermissionSet} from 'utils/space_permission_sets';

import type {Permission} from 'types/permissions';

import styles from './space_settings_modal.module.scss';

type Props = {
    spaceId: string;
    selected: readonly Permission[];
    disabled: boolean;
    customDefaultsAvailable: boolean;
    onChange: (next: Permission[]) => void;
};

/** The space-default permission tier picker shown above the individual-permission checkboxes. */
const DefaultPermissionTierSelector = ({spaceId, selected, disabled, customDefaultsAvailable, onChange}: Props) => {
    const tierLabels = usePermissionTierLabels();

    return (
        <div className={styles.presets}>
            {PERMISSION_TIERS.map((tier) => {
                const id = `space-default-tier-${tier}`;
                return (
                    <div
                        key={tier}
                        className={styles.preset}
                    >
                        <input
                            id={id}
                            type='radio'
                            name={`space-default-tier-${spaceId}`}
                            checked={samePermissionSet(TIER_PERMISSIONS[tier], selected)}
                            disabled={disabled}
                            aria-label={tierLabels[tier].label}
                            aria-describedby={`${id}-description`}
                            onChange={() => onChange([...TIER_PERMISSIONS[tier]])}
                        />
                        <label htmlFor={id}>
                            <span className={styles.presetTitle}>{tierLabels[tier].label}</span>
                            <span
                                id={`${id}-description`}
                                className={styles.presetDescription}
                            >
                                {tierLabels[tier].description}
                            </span>
                        </label>
                    </div>
                );
            })}
            {customDefaultsAvailable && (
                <span className={styles.fieldLabel}>
                    <FormattedMessage
                        id='docs.spaceSettings.permissions.customLegend'
                        defaultMessage='Or choose individual permissions:'
                    />
                </span>
            )}
        </div>
    );
};

export default DefaultPermissionTierSelector;
