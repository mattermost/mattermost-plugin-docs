// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Dialog} from '@base-ui-components/react/dialog';
import classNames from 'classnames';
import React, {createContext, useCallback, useContext, useEffect, useRef, useState} from 'react';
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

type CloseWith = (after?: () => void) => void;

const ModalCloseContext = createContext<CloseWith | undefined>(undefined);

/**
 * Dismisses the modal this component is inside, playing the exit transition and
 * only then running `after`. Buttons that both close the modal and do something —
 * a confirm, a cancel — should go through this instead of calling their handler
 * directly, or the modal is unmounted before it can animate out.
 *
 * Undefined outside a modal, so a shared control can be used in both places.
 */
export const useModalClose = () => useContext(ModalCloseContext);

type Props = {
    onClose: () => void;
    title: React.ReactNode;
    ariaLabel?: string;
    className?: string;
    headerClassName?: string;
    initialFocus?: React.RefObject<HTMLElement | null>;
    showCloseButton?: boolean;
    closeDisabled?: boolean;

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

const GenericModal = ({onClose, title, ariaLabel, className, headerClassName, initialFocus, showCloseButton = true, closeDisabled = false, titleActions, headerContent, footer, headerDivider = true, footerDivider = false, children}: Props) => {
    const {formatMessage} = useIntl();
    const closeLabel = formatMessage({id: 'docs.genericModal.close', defaultMessage: 'Close'});

    // Each level paints in its own band so the order never depends on which portal
    // happened to mount first: two slots per level, the backdrop then the popup
    // above it. A modal can be stacked two ways — pushed onto the modal stack, or
    // rendered inside another modal's JSX — and depth is the sum, since either
    // route puts one dialog above another.
    const {level: stackLevel, covered} = useDocsModalLayer();
    const nesting = useContext(ModalNestingContext);
    const modalLevel = stackLevel + nesting;
    const layerStyle = {'--docs-modal-level': modalLevel} as React.CSSProperties;
    const isCovered = covered > 0;

    // Closing is driven from here rather than by the owner unmounting us, so the
    // exit transition has somewhere to run: flip `open`, let Base UI animate, and
    // report the close only once it has finished. Whatever unmounts this modal —
    // the modal stack, or a parent's state — then does so after the animation
    // instead of cutting it off.
    const [open, setOpen] = useState(false);
    const afterCloseRef = useRef<(() => void) | undefined>(undefined);
    const openedRef = useRef(false);

    // Opened after the first paint rather than mounted open, so Base UI sees a
    // false -> true change. A dialog that is already open on its first render never
    // gets `data-starting-style` — useTransitionStatus initialises its `mounted`
    // from `open`, so the starting state is skipped — and appears with no entrance
    // animation. Every modal here is created already-open, so that was all of them.
    useEffect(() => {
        openedRef.current = true;
        setOpen(true);
    }, []);

    const closeWith = useCallback<CloseWith>((after) => {
        if (closeDisabled) {
            return;
        }
        afterCloseRef.current = after;
        setOpen(false);
    }, [closeDisabled]);

    return (
        <Dialog.Root
            open={open}
            onOpenChange={(nextOpen, details) => {
                if (nextOpen) {
                    return;
                }

                // Stacked modals are rendered as siblings, so Base UI sees no
                // parent/child relationship between them and every open dialog
                // answers the same Escape — one press would close the whole stack.
                // Only the topmost may. Modals nested through JSX are a real Base UI
                // parent/child pair and it guards them itself, so they never get here
                // covered.
                if (details.reason === 'escape-key' && isCovered) {
                    return;
                }
                closeWith();
            }}
            onOpenChangeComplete={(nextOpen) => {
                // `openedRef` guards the closed state this mounts in: without it a
                // completion reported before the modal has opened would be read as a
                // dismissal and close it on arrival.
                if (nextOpen || !openedRef.current) {
                    return;
                }
                const after = afterCloseRef.current;
                afterCloseRef.current = undefined;
                (after ?? onClose)();
            }}
        >
            <Dialog.Portal>
                {/* forceRender because Base UI renders no backdrop for a nested
                    dialog by default (DialogBackdrop: `enabled: forceRender ||
                    !nested`). Every modal above level zero uses a reduced additive
                    backdrop while the lower backdrop stays painted, so stacked and
                    React-nested dialogs transition without a gap between them. */}
                <Dialog.Backdrop
                    forceRender={true}
                    className={classNames(styles.backdrop, {
                        [styles.backdropNested]: modalLevel > 0,
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
                        <ModalNestingContext.Provider value={nesting + 1}>
                            <ModalCloseContext.Provider value={closeWith}>
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
                            </ModalCloseContext.Provider>
                        </ModalNestingContext.Provider>
                    </Dialog.Popup>
                </div>
            </Dialog.Portal>
        </Dialog.Root>
    );
};

export default GenericModal;
