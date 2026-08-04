// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Dialog} from '@base-ui-components/react/dialog';
import classNames from 'classnames';
import React, {createContext, useContext} from 'react';
import {useIntl} from 'react-intl';

import CloseIcon from '@mattermost/compass-icons/components/close';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {useDocsModalLayer} from 'components/modals';

import styles from './generic_modal.module.scss';

// How many modals this one is rendered inside. A modal opened by rendering it in
// another's JSX (rather than through `openDocsModal`) stacks through Base UI's
// nesting instead of through the stack, so it needs its own count to land in the
// right paint band — Base UI's own nesting context isn't exported.
const ModalNestingContext = createContext(0);

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

    // Each level paints in its own band so the order never depends on which portal
    // happened to mount first: two slots per level, the backdrop then the popup
    // above it. A modal can be stacked two ways — pushed onto the modal stack, or
    // rendered inside another modal's JSX — and depth is the sum, since either
    // route puts one dialog above another.
    const {level: stackLevel, covered} = useDocsModalLayer();
    const nesting = useContext(ModalNestingContext);
    const layerStyle = {'--docs-modal-level': stackLevel + nesting} as React.CSSProperties;
    const isCovered = covered > 0;

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
                {/* forceRender because Base UI renders no backdrop for a nested
                    dialog by default (DialogBackdrop: `enabled: forceRender ||
                    !nested`), which left a modal opened from inside another with
                    nothing to dim or click away on. Its alpha is reduced instead,
                    since the modal below already dims the app. */}
                <Dialog.Backdrop
                    forceRender={true}
                    className={classNames(styles.backdrop, {
                        [styles.backdropNested]: nesting > 0,
                        [styles.backdropCovered]: isCovered,
                    })}
                    style={layerStyle}
                />

                {/* Flex centering container rendered after the backdrop, so the
                    popup paints above it within this level's band — and no
                    centering transform on the popup itself. */}
                <div
                    className={styles.viewport}
                    style={layerStyle}
                >
                    <Dialog.Popup
                        className={classNames(styles.modal, {[styles.modalCovered]: isCovered}, className)}
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
                        <ModalNestingContext.Provider value={nesting + 1}>
                            {children}
                            {footer && <div className={classNames(styles.footer, {[styles.footerDivider]: footerDivider})}>{footer}</div>}
                        </ModalNestingContext.Provider>
                    </Dialog.Popup>
                </div>
            </Dialog.Portal>
        </Dialog.Root>
    );
};

export default GenericModal;
