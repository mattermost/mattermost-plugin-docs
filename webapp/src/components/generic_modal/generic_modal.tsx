// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Dialog} from '@base-ui-components/react/dialog';
import classNames from 'classnames';
import React from 'react';
import {useIntl} from 'react-intl';

import CloseIcon from '@mattermost/compass-icons/components/close';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import styles from './generic_modal.module.scss';

type Props = {
    onClose: () => void;
    title: React.ReactNode;
    ariaLabel?: string;
    className?: string;
    headerClassName?: string;
    initialFocus?: React.RefObject<HTMLElement | null>;
    showCloseButton?: boolean;
    headerContent?: React.ReactNode;
    footer?: React.ReactNode;
    children: React.ReactNode;
};

const GenericModal = ({onClose, title, ariaLabel, className, headerClassName, initialFocus, showCloseButton = true, headerContent, footer, children}: Props) => {
    const {formatMessage} = useIntl();
    const closeLabel = formatMessage({id: 'generic_modal.close', defaultMessage: 'Close'});

    return (
        <Dialog.Root
            open={true}
            onOpenChange={(nextOpen) => {
                if (!nextOpen) {
                    onClose();
                }
            }}
        >
            <Dialog.Portal>
                <Dialog.Backdrop className={styles.backdrop}/>

                {/* Flex centering container rendered after the backdrop, so the
                    popup paints above it by DOM order — no z-index needed, and
                    no centering transform on the popup itself. */}
                <div className={styles.viewport}>
                    <Dialog.Popup
                        className={classNames(styles.modal, className)}
                        initialFocus={initialFocus}
                        aria-label={ariaLabel}
                    >
                        <div className={classNames(styles.header, headerClassName)}>
                            <div className={styles.titleRow}>
                                <Dialog.Title render={<h1 className={styles.title}/>}>
                                    {title}
                                </Dialog.Title>
                                {showCloseButton && (
                                    <WithTooltip title={closeLabel}>
                                        <Dialog.Close
                                            render={(
                                                <button
                                                    type='button'
                                                    className={styles.close}
                                                    aria-label={closeLabel}
                                                />
                                            )}
                                        >
                                            <CloseIcon size={24}/>
                                        </Dialog.Close>
                                    </WithTooltip>
                                )}
                            </div>
                            {headerContent}
                        </div>
                        {children}
                        {footer && <div className={styles.footer}>{footer}</div>}
                    </Dialog.Popup>
                </div>
            </Dialog.Portal>
        </Dialog.Root>
    );
};

export default GenericModal;
