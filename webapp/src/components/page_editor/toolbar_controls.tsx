// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Editor} from '@tiptap/core';
import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import {
    AlertCircleOutlineIcon,
    AlertOutlineIcon,
    CheckCircleOutlineIcon,
    CloseCircleOutlineIcon,
    InformationOutlineIcon,
    PinOutlineIcon,
} from '@mattermost/compass-icons/components';

import type {CalloutType} from './callout_extension';
import {CALLOUT_TYPES} from './callout_extension';
import styles from './toolbar_controls.module.scss';

const CALLOUT_ICONS: Record<CalloutType, typeof InformationOutlineIcon> = {
    info: InformationOutlineIcon,
    note: AlertCircleOutlineIcon,
    success: CheckCircleOutlineIcon,
    warning: AlertOutlineIcon,
    error: CloseCircleOutlineIcon,
};

type PinProps = {
    pinned: boolean;
    onToggle: () => void;
};

export const PinToolbarControl = ({pinned, onToggle}: PinProps) => {
    const {formatMessage} = useIntl();
    const label = pinned ? formatMessage({id: 'docs.editor.unpinToolbar', defaultMessage: 'Unpin toolbar'}) : formatMessage({id: 'docs.editor.pinToolbar', defaultMessage: 'Pin toolbar to top'});

    return (
        <button
            type='button'
            className={pinned ? `${styles.control} ${styles.active}` : styles.control}
            onClick={onToggle}
            aria-label={label}
            title={label}
            aria-pressed={pinned}
        >
            <PinOutlineIcon size={18}/>
        </button>
    );
};

type CalloutProps = {
    getEditor: () => unknown;
};

export const CalloutControl = ({getEditor}: CalloutProps) => {
    const {formatMessage} = useIntl();
    const [open, setOpen] = useState(false);
    const wrapperRef = useRef<HTMLDivElement>(null);
    const triggerRef = useRef<HTMLButtonElement>(null);

    useEffect(() => {
        if (!open) {
            return undefined;
        }
        const onDocumentClick = (e: MouseEvent) => {
            if (!wrapperRef.current?.contains(e.target as globalThis.Node)) {
                setOpen(false);
            }
        };
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.stopPropagation();
                setOpen(false);
                triggerRef.current?.focus();
            }
        };
        document.addEventListener('click', onDocumentClick);
        document.addEventListener('keydown', onKeyDown, true);
        return () => {
            document.removeEventListener('click', onDocumentClick);
            document.removeEventListener('keydown', onKeyDown, true);
        };
    }, [open]);

    const insert = useCallback((type: CalloutType) => {
        const editor = getEditor() as Editor | null;
        editor?.chain().focus().toggleCallout(type).run();
        setOpen(false);
    }, [getEditor]);

    const label = formatMessage({id: 'docs.editor.callout', defaultMessage: 'Call out alert'});

    return (
        <div
            ref={wrapperRef}
            className={styles.menuWrapper}
        >
            <button
                ref={triggerRef}
                type='button'
                className={styles.control}
                onClick={() => setOpen((prev) => !prev)}
                aria-label={label}
                title={label}
                aria-expanded={open}
                aria-haspopup='menu'
            >
                <InformationOutlineIcon size={18}/>
            </button>

            {open && (
                <div
                    className={styles.menu}
                    role='menu'
                >
                    {CALLOUT_TYPES.map((type) => {
                        const Icon = CALLOUT_ICONS[type];
                        return (
                            <button
                                key={type}
                                type='button'
                                role='menuitem'
                                className={styles.menuItem}
                                onClick={() => insert(type)}
                            >
                                <Icon size={16}/>
                                {CALLOUT_LABELS[type](formatMessage)}
                            </button>
                        );
                    })}
                </div>
            )}
        </div>
    );
};

type Formatter = ReturnType<typeof useIntl>['formatMessage'];

const CALLOUT_LABELS: Record<CalloutType, (f: Formatter) => string> = {
    info: (f) => f({id: 'docs.editor.calloutInfo', defaultMessage: 'Info'}),
    note: (f) => f({id: 'docs.editor.calloutNote', defaultMessage: 'Note'}),
    success: (f) => f({id: 'docs.editor.calloutSuccess', defaultMessage: 'Success'}),
    warning: (f) => f({id: 'docs.editor.calloutWarning', defaultMessage: 'Warning'}),
    error: (f) => f({id: 'docs.editor.calloutError', defaultMessage: 'Error'}),
};
