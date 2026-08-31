// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import Menu from 'components/menu/menu';

import type {SpaceViewAccess} from 'types/permissions';

import styles from './share_space_modal.module.scss';

type Props = {
    viewAccess: SpaceViewAccess;
    disabled: boolean;
    disabledReason?: string;

    /** The caller may not change visibility: state it, and offer no control. */
    readOnly?: boolean;
    onChange: (next: SpaceViewAccess) => void;
};

/** The Share footer's Public/Private picker. */
const VisibilityMenu = ({viewAccess, disabled, disabledReason, readOnly, onChange}: Props) => {
    const {formatMessage} = useIntl();
    const isOpen = viewAccess === 'open';

    const label = isOpen ? (
        <FormattedMessage
            id='docs.share.visibility.public'
            defaultMessage='Public'
        />
    ) : (
        <FormattedMessage
            id='docs.share.visibility.private'
            defaultMessage='Private'
        />
    );

    const hint = (
        <span className={styles.accessHint}>
            {isOpen ? (
                <FormattedMessage
                    id='docs.share.visibility.publicHint'
                    defaultMessage='Any team member can view'
                />
            ) : (
                <FormattedMessage
                    id='docs.share.visibility.privateHint'
                    defaultMessage='Only invited members'
                />
            )}
        </span>
    );

    if (readOnly) {
        return (
            <div className={styles.accessLeft}>
                <span className={styles.accessStatic}>
                    {isOpen ? <GlobeIcon size={16}/> : <LockOutlineIcon size={16}/>}
                    {label}
                </span>
                {hint}
            </div>
        );
    }

    const trigger = (
        <button
            type='button'
            className={styles.accessTrigger}
            disabled={disabled}
            title={disabled ? disabledReason : undefined}
        >
            {isOpen ? <GlobeIcon size={16}/> : <LockOutlineIcon size={16}/>}
            {label}
            <ChevronDownIcon size={16}/>
        </button>
    );

    return (
        <div className={styles.accessLeft}>
            <Menu
                ariaLabel={formatMessage({id: 'docs.share.visibility.menuLabel', defaultMessage: 'Space visibility'})}
                trigger={trigger}
            >
                <Menu.RadioGroup
                    value={viewAccess}
                    onValueChange={(next) => onChange(next as SpaceViewAccess)}
                >
                    <Menu.RadioItem
                        value='open'
                        leadingIcon={<GlobeIcon size={16}/>}
                    >
                        <FormattedMessage
                            id='docs.share.visibility.public'
                            defaultMessage='Public'
                        />
                    </Menu.RadioItem>
                    <Menu.RadioItem
                        value='private'
                        leadingIcon={<LockOutlineIcon size={16}/>}
                    >
                        <FormattedMessage
                            id='docs.share.visibility.private'
                            defaultMessage='Private'
                        />
                    </Menu.RadioItem>
                </Menu.RadioGroup>
            </Menu>
            {hint}
        </div>
    );
};

export default VisibilityMenu;
