// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Toast} from '@base-ui-components/react/toast';
import classNames from 'classnames';
import React from 'react';
import {useIntl} from 'react-intl';

import AlertCircleOutlineIcon from '@mattermost/compass-icons/components/alert-circle-outline';
import AlertOutlineIcon from '@mattermost/compass-icons/components/alert-outline';
import CheckCircleOutlineIcon from '@mattermost/compass-icons/components/check-circle-outline';
import CloseIcon from '@mattermost/compass-icons/components/close';
import InformationOutlineIcon from '@mattermost/compass-icons/components/information-outline';
import type IconProps from '@mattermost/compass-icons/components/props';

import styles from './docs_toaster.module.scss';
import {docsToastManager} from './toast';
import type {DocsToastVariant} from './toast';

const VARIANT_ICONS: Record<DocsToastVariant, React.FC<IconProps>> = {
    success: CheckCircleOutlineIcon,
    error: AlertCircleOutlineIcon,
    warning: AlertOutlineIcon,
    info: InformationOutlineIcon,
};

const variantOf = (type: string | undefined): DocsToastVariant => (
    type && type in VARIANT_ICONS ? type as DocsToastVariant : 'info'
);

const DocsToastList = () => {
    const {toasts} = Toast.useToastManager();
    const {formatMessage} = useIntl();

    return (
        <Toast.Viewport className={styles.viewport}>
            {toasts.map((toast) => {
                const variant = variantOf(toast.type);
                const Icon = VARIANT_ICONS[variant];

                return (
                    <Toast.Root
                        key={toast.id}
                        toast={toast}
                        className={classNames(styles.toast, styles[variant])}
                    >
                        <span className={styles.icon}>
                            <Icon size={20}/>
                        </span>
                        <div className={styles.body}>
                            <Toast.Title className={styles.title}/>
                            <Toast.Description className={styles.description}/>
                        </div>
                        <Toast.Close
                            className={styles.close}
                            aria-label={formatMessage({id: 'docs.toast.close', defaultMessage: 'Close'})}
                        >
                            <CloseIcon size={16}/>
                        </Toast.Close>
                    </Toast.Root>
                );
            })}
        </Toast.Viewport>
    );
};

/**
 * Renders the Docs product's toasts. Mount exactly once, at the Docs root; all
 * toasts are raised through the `toast` API or `useToast()`.
 */
const DocsToaster = () => (
    <Toast.Provider toastManager={docsToastManager}>
        <Toast.Portal>
            <DocsToastList/>
        </Toast.Portal>
    </Toast.Provider>
);

export default DocsToaster;
