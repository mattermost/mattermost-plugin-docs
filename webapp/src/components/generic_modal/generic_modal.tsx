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

    /**
     * Content aligned to the right of the title, before the close button — for
     * actions that belong to the header rather than the body or footer.
     */
    titleActions?: React.ReactNode;
    headerContent?: React.ReactNode;
    footer?: React.ReactNode;

    /** Divider under the header. On by default; opt out for minimal modals. */
    headerDivider?: boolean;

    /** Renders a divider between the body and the footer actions. */
    footerDivider?: boolean;
    children: React.ReactNode;
};

const GenericModal = ({onClose, title, ariaLabel, className, headerClassName, initialFocus, showCloseButton = true, titleActions, headerContent, footer, headerDivider = true, footerDivider = false, children}: Props) => {
    const {formatMessage} = useIntl();
    const closeLabel = formatMessage({id: 'docs.genericModal.close', defaultMessage: 'Close'});

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
                        <div className={classNames(styles.header, {[styles.headerDivider]: headerDivider}, headerClassName)}>
                            <div className={styles.titleRow}>
                                <Dialog.Title render={<h1 className={styles.title}/>}>
                                    {title}
                                </Dialog.Title>
                                {titleActions != null && <div className={styles.titleActions}>{titleActions}</div>}
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
                        {footer && <div className={classNames(styles.footer, {[styles.footerDivider]: footerDivider})}>{footer}</div>}
                    </Dialog.Popup>
                </div>
            </Dialog.Portal>
        </Dialog.Root>
    );
};

export default GenericModal;
