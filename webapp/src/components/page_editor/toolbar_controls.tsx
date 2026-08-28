// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {autoUpdate, flip, FloatingPortal, offset, shift, useFloating} from '@floating-ui/react';
import type {Editor} from '@tiptap/core';
import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import {
    AlertCircleOutlineIcon,
    AlertOutlineIcon,
    CheckCircleOutlineIcon,
    CloseCircleOutlineIcon,
    DotsHorizontalIcon,
    InformationOutlineIcon,
    PinIcon,
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

type MenuControlProps = {
    label: string;
    icon: React.ReactNode;
    children: (close: () => void) => React.ReactNode;
};

// The menu is portalled out of the bar: the host formatting bar clips its own
// track, so a menu rendered inside it is cut off at the bar's edge.
const MenuControl = ({label, icon, children}: MenuControlProps) => {
    const [open, setOpen] = useState(false);
    const triggerRef = useRef<HTMLButtonElement | null>(null);
    const menuRef = useRef<HTMLDivElement | null>(null);

    const {refs, floatingStyles} = useFloating({
        open,
        placement: 'bottom-start',
        middleware: [offset(4), flip(), shift({padding: 8})],
        whileElementsMounted: autoUpdate,
    });

    const {setReference, setFloating} = refs;

    const setTrigger = useCallback((node: HTMLButtonElement | null) => {
        triggerRef.current = node;
        setReference(node);
    }, [setReference]);

    const setMenu = useCallback((node: HTMLDivElement | null) => {
        menuRef.current = node;
        setFloating(node);
    }, [setFloating]);

    const close = useCallback(() => setOpen(false), []);

    useEffect(() => {
        if (!open) {
            return undefined;
        }
        const onDocumentClick = (e: MouseEvent) => {
            const target = e.target as globalThis.Node;
            if (!menuRef.current?.contains(target) && !triggerRef.current?.contains(target)) {
                setOpen(false);
            }
        };
        document.addEventListener('click', onDocumentClick);
        return () => document.removeEventListener('click', onDocumentClick);
    }, [open]);

    const onKeyDown = useCallback((e: React.KeyboardEvent) => {
        if (e.key === 'Escape' && open) {
            e.stopPropagation();
            setOpen(false);
            triggerRef.current?.focus();
        }
    }, [open]);

    return (
        <>
            <button
                ref={setTrigger}
                type='button'
                className={styles.control}
                onClick={() => setOpen((prev) => !prev)}
                onKeyDown={onKeyDown}
                aria-label={label}
                title={label}
                aria-expanded={open}
                aria-haspopup='menu'
            >
                {icon}
            </button>

            {open && (
                <FloatingPortal>
                    <div
                        ref={setMenu}
                        className={styles.menu}
                        style={floatingStyles}
                        role='menu'
                        onKeyDown={onKeyDown}

                        // Keeps the caret where it is: a command run from here acts on
                        // the selection the menu was opened for.
                        onMouseDown={(e) => e.preventDefault()}
                    >
                        {children(close)}
                    </div>
                </FloatingPortal>
            )}
        </>
    );
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
            className={`${styles.control} ${pinned ? styles.active : ''} docs-pin-control`}
            onClick={onToggle}
            aria-label={label}
            title={label}
            aria-pressed={pinned}
        >
            {pinned ? <PinIcon size={18}/> : <PinOutlineIcon size={18}/>}
        </button>
    );
};

type OverflowProps = {
    onPin: () => void;
};

export const OverflowControl = ({onPin}: OverflowProps) => {
    const {formatMessage} = useIntl();

    return (
        <MenuControl
            label={formatMessage({id: 'docs.editor.moreOptions', defaultMessage: 'More options'})}
            icon={<DotsHorizontalIcon size={18}/>}
        >
            {(close) => (
                <button
                    type='button'
                    role='menuitem'
                    className={styles.menuItem}
                    onClick={() => {
                        onPin();
                        close();
                    }}
                >
                    <PinOutlineIcon size={16}/>
                    {formatMessage({id: 'docs.editor.pinToolbar', defaultMessage: 'Pin toolbar to top'})}
                </button>
            )}
        </MenuControl>
    );
};

type CalloutProps = {
    getEditor: () => unknown;
};

export const CalloutControl = ({getEditor}: CalloutProps) => {
    const {formatMessage} = useIntl();

    const insert = useCallback((type: CalloutType) => {
        const editor = getEditor() as Editor | null;
        const chain = editor?.chain().focus();

        if (chain && typeof chain.toggleCallout === 'function') {
            chain.toggleCallout(type).run();
        }
    }, [getEditor]);

    return (
        <MenuControl
            label={formatMessage({id: 'docs.editor.callout', defaultMessage: 'Insert callout'})}
            icon={<InformationOutlineIcon size={18}/>}
        >
            {(close) => CALLOUT_TYPES.map((type) => {
                const Icon = CALLOUT_ICONS[type];
                return (
                    <button
                        key={type}
                        type='button'
                        role='menuitem'
                        className={styles.menuItem}
                        onClick={() => {
                            insert(type);
                            close();
                        }}
                    >
                        <Icon size={16}/>
                        {CALLOUT_LABELS[type](formatMessage)}
                    </button>
                );
            })}
        </MenuControl>
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
