// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import AlertCircleOutlineIcon from '@mattermost/compass-icons/components/alert-circle-outline';
import AlertOutlineIcon from '@mattermost/compass-icons/components/alert-outline';
import CheckCircleOutlineIcon from '@mattermost/compass-icons/components/check-circle-outline';
import InformationOutlineIcon from '@mattermost/compass-icons/components/information-outline';
import type IconProps from '@mattermost/compass-icons/components/props';

import styles from './section_notice.module.scss';

export type SectionNoticeVariant = 'info' | 'success' | 'error' | 'warning';

const VARIANT_ICONS: Record<SectionNoticeVariant, React.FC<IconProps>> = {
    info: InformationOutlineIcon,
    success: CheckCircleOutlineIcon,
    error: AlertOutlineIcon,
    warning: AlertCircleOutlineIcon,
};

type Props = {
    variant?: SectionNoticeVariant;
    title?: React.ReactNode;
    children?: React.ReactNode;
    className?: string;
    role?: React.AriaRole;
};

/**
 * In-page banner matching Mattermost Section Notice: tinted surface, 1px border,
 * leading icon, and optional title + body. Actions and dismiss are omitted until
 * a notice needs them.
 */
const SectionNotice = ({variant = 'info', title, children, className, role}: Props) => {
    const Icon = VARIANT_ICONS[variant];

    return (
        <div
            className={classNames(styles.notice, styles[variant], className)}
            role={role}
        >
            <span
                className={styles.icon}
                aria-hidden={true}
            >
                <Icon size={20}/>
            </span>
            <div className={styles.body}>
                {title && <div className={styles.title}>{title}</div>}
                {children && <div className={styles.description}>{children}</div>}
            </div>
        </div>
    );
};

export default SectionNotice;
