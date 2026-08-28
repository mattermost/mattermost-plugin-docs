// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {usePermissionLabels, usePermissionTierLabels} from 'hooks/permission_labels';
import React from 'react';
import {useIntl} from 'react-intl';
import {PERMISSION_TIERS, TIER_PERMISSIONS, summarizePermissions, type PermissionTier} from 'utils/space_permission_sets';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';

import {Button} from 'components/form_controls/button';
import Menu from 'components/menu/menu';

import type {Permission} from 'types/permissions';
import {DEFAULT_PERMISSION_ORDER} from 'types/permissions';

import styles from './share_space_modal.module.scss';

type Props = {
    defaults: Permission[];
    disabled: boolean;
    disabledReason?: string;
    customDefaultsAvailable: boolean;
    onChange: (next: Permission[]) => void;
};

/** The Share footer's space-default permissions picker: the named tiers first, refined below
 * them by the individual permissions when the server licence allows a custom combination. */
const DefaultPermissionsMenu = ({defaults, disabled, disabledReason, customDefaultsAvailable, onChange}: Props) => {
    const {formatMessage} = useIntl();
    const tierLabels = usePermissionTierLabels();
    const permissionLabels = usePermissionLabels();
    const defaultTier = summarizePermissions(defaults);

    const trigger = (
        <Button
            type='button'
            emphasis='quaternary'
            size='sm'
            className={styles.canView}
            disabled={disabled}
            title={disabled ? disabledReason : undefined}
        >
            {tierLabels[defaultTier].label}
            <ChevronDownIcon size={16}/>
        </Button>
    );

    return (
        <Menu
            ariaLabel={formatMessage({id: 'docs.share.access.menuLabel', defaultMessage: 'Default permissions for everyone with access'})}
            align='right'
            trigger={trigger}
        >
            <Menu.RadioGroup
                value={defaultTier}
                onValueChange={(tier) => onChange([...TIER_PERMISSIONS[tier as PermissionTier]])}
            >
                {PERMISSION_TIERS.map((tier) => (
                    <Menu.RadioItem
                        key={tier}
                        value={tier}
                        secondaryLabel={tierLabels[tier].description}
                        disabled={disabled}
                    >
                        {tierLabels[tier].label}
                    </Menu.RadioItem>
                ))}
            </Menu.RadioGroup>
            {customDefaultsAvailable && (
                <>
                    <Menu.Separator/>
                    {DEFAULT_PERMISSION_ORDER.map((permission) => (
                        <Menu.CheckboxItem
                            key={permission}
                            checked={defaults.includes(permission)}
                            disabled={disabled}
                            secondaryLabel={disabled ? disabledReason : undefined}
                            onCheckedChange={(checked) => {
                                if (disabled) {
                                    return;
                                }

                                const next = DEFAULT_PERMISSION_ORDER.filter((option) => (
                                    option === permission ? checked : defaults.includes(option)
                                ));
                                onChange(next);
                            }}
                        >
                            {permissionLabels[permission]}
                        </Menu.CheckboxItem>
                    ))}
                </>
            )}
        </Menu>
    );
};

export default DefaultPermissionsMenu;
