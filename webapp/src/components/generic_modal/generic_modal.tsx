// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Dialog} from '@base-ui-components/react/dialog';
import classNames from 'classnames';
import React from 'react';
import {useIntl} from 'react-intl';

import CloseIcon from '@mattermost/compass-icons/components/close';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import './generic_modal.scss';

type Props = {
    onClose: () => void;
    title: React.ReactNode;

    // Accessible name for the dialog; defaults to letting Base UI derive it
    // from the title.
    ariaLabel?: string;

    // Modifier class on the popup for per-modal layout (size, position).
    className?: string;

    // Element focused when the modal opens.
    initialFocus?: React.RefObject<HTMLElement | null>;
    showCloseButton?: boolean;

    // Extra header content rendered under the title row (e.g. a search field).
    headerContent?: React.ReactNode;

    // GenericModal body.
    children: React.ReactNode;
};

// Reusable modal built on Base UI's Dialog: portaled, focus-trapped, scroll-
// locked, dismissable (Esc / outside press), with the standard Mattermost modal
// chrome (title row + 40x40 close button). Consumers supply the title, optional
// header content, and the body; per-modal sizing/position comes via className.
const GenericModal = ({onClose, title, ariaLabel, className, initialFocus, showCloseButton = true, headerContent, children}: Props) => {
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
                <Dialog.Backdrop className='GenericModal__backdrop'/>
                <Dialog.Popup
                    className={classNames('GenericModal', className)}
                    initialFocus={initialFocus}
                    aria-label={ariaLabel}
                >
                    <div className='GenericModal__header'>
                        <div className='GenericModal__titleRow'>
                            <Dialog.Title render={<h1 className='GenericModal__title'/>}>
                                {title}
                            </Dialog.Title>
                            {showCloseButton && (
                                <WithTooltip title={closeLabel}>
                                    <Dialog.Close
                                        render={(
                                            <button
                                                type='button'
                                                className='GenericModal__close'
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
                </Dialog.Popup>
            </Dialog.Portal>
        </Dialog.Root>
    );
};

export default GenericModal;
