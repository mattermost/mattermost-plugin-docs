// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {autoUpdate, flip, FloatingPortal, offset, shift, size, useFloating} from '@floating-ui/react';
import classNames from 'classnames';
import {useSlashMenu} from 'hooks/slash_menu';
import React, {useCallback, useEffect, useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import styles from './slash_menu.module.scss';

const GAP = 4;
const MARGIN = 8;
const MAX_HEIGHT = 320;

type Props = {
    editing: boolean;
    getEditor: () => unknown;
    surfaceRef: React.RefObject<HTMLElement>;
};

const SlashMenu = ({editing, getEditor, surfaceRef}: Props) => {
    const {formatMessage} = useIntl();
    const {open, blocks, active, setActive, select, close, rect} = useSlashMenu({editing, getEditor});

    const middleware = useMemo(() => [
        offset(GAP),
        flip({padding: MARGIN}),
        shift({padding: MARGIN}),
        size({
            padding: MARGIN,
            apply: ({availableHeight, elements}) => {
                elements.floating.style.maxHeight = `${Math.min(MAX_HEIGHT, availableHeight)}px`;
            },
        }),
    ], []);

    const {refs, floatingStyles} = useFloating({
        open,
        placement: 'bottom-start',
        strategy: 'fixed',
        middleware,
        whileElementsMounted: autoUpdate,
    });

    const {setReference, setFloating} = refs;

    useEffect(() => {
        setReference(open ? {
            contextElement: surfaceRef.current ?? undefined,
            getBoundingClientRect: () => rect() ?? new DOMRect(),
        } : null);
    }, [setReference, open, rect, surfaceRef]);

    // The list scrolls, so keyboard navigation has to drag the viewport along with it.
    const setActiveItem = useCallback((node: HTMLButtonElement | null) => {
        node?.scrollIntoView({block: 'nearest'});
    }, []);

    const onKeyDown = useCallback((event: KeyboardEvent) => {
        if (!open) {
            return;
        }

        const move = (delta: number) => {
            setActive((active + delta + blocks.length) % blocks.length);
        };

        switch (event.key) {
        case 'ArrowDown':
            move(1);
            break;
        case 'ArrowUp':
            move(-1);
            break;
        case 'Enter':
        case 'Tab':
            select();
            break;
        case 'Escape':
            close();
            break;
        default:
            return;
        }

        event.preventDefault();
        event.stopPropagation();
    }, [open, active, blocks.length, setActive, select, close]);

    useEffect(() => {
        const surface = surfaceRef.current;
        if (!surface || !open) {
            return undefined;
        }

        surface.addEventListener('keydown', onKeyDown, true);
        return () => surface.removeEventListener('keydown', onKeyDown, true);
    }, [surfaceRef, open, onKeyDown]);

    if (!open) {
        return null;
    }

    return (
        <FloatingPortal>
            <div
                ref={setFloating}
                className={styles.menu}
                style={floatingStyles}
                role='listbox'
                aria-label={formatMessage({id: 'docs.editor.insert.menu', defaultMessage: 'Insert a block'})}
            >
                <div className={styles.group}>
                    <FormattedMessage
                        id='docs.editor.insert.basicBlocks'
                        defaultMessage='Basic blocks'
                    />
                </div>

                {blocks.map((block, index) => (
                    <button
                        key={block.id}
                        ref={index === active ? setActiveItem : undefined}
                        type='button'
                        role='option'
                        aria-selected={index === active}
                        className={classNames(styles.item, {[styles.active]: index === active})}
                        onMouseEnter={() => setActive(index)}

                        // Keeps the caret in the document, so the block lands where the
                        // author typed the trigger.
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => select(index)}
                    >
                        <span className={styles.icon}>{block.icon}</span>
                        {formatMessage(block.title)}
                    </button>
                ))}
            </div>
        </FloatingPortal>
    );
};

export default SlashMenu;
