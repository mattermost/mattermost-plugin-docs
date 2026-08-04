// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DropIndicator} from '@atlaskit/pragmatic-drag-and-drop-react-drop-indicator/box';
import classNames from 'classnames';
import {useIsFavorite} from 'hooks/favorites';
import {useTextOverflow} from 'hooks/text_overflow';
import React, {useState} from 'react';
import {createPortal} from 'react-dom';
import {useIntl} from 'react-intl';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ChevronRightIcon from '@mattermost/compass-icons/components/chevron-right';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import FileTextOutlineIcon from '@mattermost/compass-icons/components/file-text-outline';
import FolderOutlineIcon from '@mattermost/compass-icons/components/folder-outline';
import StarIcon from '@mattermost/compass-icons/components/star';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import type {PageNode} from 'store/page_tree';

import PageMenu from 'components/page_menu/page_menu';
import Spacer from 'components/spacer/spacer';

import {MAX_PAGE_DEPTH} from './depth';
import {usePageDragDrop} from './dnd/use_page_drag_drop';
import PageDragPreview from './page_drag_preview';
import styles from './page_tree_node.module.scss';

const INDENT_STEP = 12;

type Props = {
    node: PageNode;
    activePageId?: string;
    collapsed: Set<string>;
    descendants: Map<string, Set<string>>;
    subtreeHeights: Map<string, number>;
    draggingId: string | null;
    dndEnabled: boolean;

    // The one row in the whole tree that is tabbable (roving tabindex).
    tabStopId?: string;
    onSelect: (pageId: string) => void;
    onToggle: (pageId: string) => void;
    onSetCollapsed: (ids: string[], collapsed: boolean) => void;
    onFocusRow: (pageId: string) => void;
    onRowKeyDown: (event: React.KeyboardEvent, pageId: string) => void;
    registerRow: (pageId: string, element: HTMLElement | null) => void;
};

const PageTreeNode = ({node, activePageId, collapsed, descendants, subtreeHeights, draggingId, dndEnabled, tabStopId, onSelect, onToggle, onSetCollapsed, onFocusRow, onRowKeyDown, registerRow}: Props) => {
    const {formatMessage} = useIntl();
    const [element, setElement] = useState<HTMLDivElement | null>(null);
    const [menuOpen, setMenuOpen] = useState(false);
    const [titleOverflowing, titleRef] = useTextOverflow();

    // Narrow subscription: only this page's own favorite state re-renders the row.
    const favorited = useIsFavorite('page', node.page.id);
    const {page, children, depth} = node;
    const hasChildren = children.length > 0;
    const isCollapsed = collapsed.has(page.id);

    // A drag can never drop into its own subtree; those rows aren't drop targets
    // (truthful canDrop), so mark them as an invalid zone for the whole drag.
    const inDraggedSubtree = draggingId !== null && draggingId !== page.id && (descendants.get(draggingId)?.has(page.id) ?? false);
    const isActive = page.id === activePageId;

    const toggleLabel = isCollapsed ? formatMessage({id: 'docs.pageTree.expand', defaultMessage: 'Expand {title}'}, {title: page.title}) : formatMessage({id: 'docs.pageTree.collapse', defaultMessage: 'Collapse {title}'}, {title: page.title});
    const menuLabel = formatMessage({id: 'docs.pageTree.menu', defaultMessage: 'Page options for {title}'}, {title: page.title});
    const favoriteLabel = formatMessage({id: 'docs.pageTree.favorited', defaultMessage: 'Favorited'});

    const {dragging, dropTarget, blocked, previewContainer} = usePageDragDrop({
        pageId: page.id,
        element,
        enabled: dndEnabled,
        canDrop: (sourceId, mode) => {
            if (descendants.get(sourceId)?.has(page.id)) {
                return false;
            }

            // Reparenting nests the source one level under this row; reordering
            // makes it a sibling. Either way its own subtree rides along, so the
            // deepest moved node must still fit under the level cap.
            const movedDepth = mode === 'reparent' ? depth + 1 : depth;
            return movedDepth + (subtreeHeights.get(sourceId) ?? 0) <= MAX_PAGE_DEPTH;
        },
    });

    // Shift-click expands/collapses the whole subtree: apply this node's next
    // state to it and all of its descendants at once.
    const toggleRecursive = () => {
        const ids = [page.id, ...(descendants.get(page.id) ?? [])];
        onSetCollapsed(ids, !isCollapsed);
    };

    const onRowClick = (event: React.MouseEvent<HTMLDivElement>) => {
        if (event.shiftKey && hasChildren) {
            toggleRecursive();
            return;
        }
        onSelect(page.id);
    };

    const onChevronClick = (event: React.MouseEvent<HTMLButtonElement>) => {
        event.stopPropagation();
        if (event.shiftKey) {
            toggleRecursive();
            return;
        }
        onToggle(page.id);
    };

    // Treeitems nest, so key and focus events from a descendant row bubble through
    // every ancestor treeitem. Only the row the event actually targeted may act on
    // it, or one keypress would be handled once per level.
    const isOwnEvent = (event: React.SyntheticEvent) => event.target === event.currentTarget;

    // Shift+F10 and the Menu key are the platform conventions for "open this
    // row's context menu"; the trigger button itself stays out of the tab order
    // so the tree keeps a single tab stop.
    const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
        if (!isOwnEvent(event)) {
            return;
        }

        if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
            event.preventDefault();
            setMenuOpen(true);
            return;
        }
        onRowKeyDown(event, page.id);
    };

    const onFocus = (event: React.FocusEvent<HTMLDivElement>) => {
        if (isOwnEvent(event)) {
            onFocusRow(page.id);
        }
    };

    // Three elements, three jobs: the hit wrapper is the drag/drop target (its box
    // includes the spacing around the row, so a drag between rows still lands in
    // one), the row inside it is what's painted, and the outer treeitem takes focus.
    const setRowElement = (next: HTMLDivElement | null) => setElement(next);

    const setNodeElement = (next: HTMLDivElement | null) => registerRow(page.id, next);

    const PageGlyph = page.type === 'page_folder' ? FolderOutlineIcon : FileTextOutlineIcon;

    // Only a clipped title gets a tooltip; a fully visible one would just repeat
    // the text under the pointer.
    const title = (
        <WithTooltip
            title={page.title}
            disabled={!titleOverflowing}
        >
            <span
                ref={titleRef}
                className={styles.title}
            >
                {page.title}
            </span>
        </WithTooltip>
    );

    return (

        // The treeitem is this outer element, not the visible row, so the child
        // `role="group"` below is a real descendant of it — the tree pattern needs
        // that ownership, and an intervening `role="none"` wrapper would flatten
        // both into siblings in the accessibility tree.
        <div
            ref={setNodeElement}
            className={styles.node}
            role='treeitem'
            tabIndex={tabStopId === page.id ? 0 : -1}
            aria-level={depth + 1}
            aria-selected={isActive}
            aria-expanded={hasChildren ? !isCollapsed : undefined}
            onKeyDown={onKeyDown}
            onFocus={onFocus}
        >
            <div
                ref={setRowElement}
                className={styles.rowHit}
            >
                <div
                    className={classNames(styles.row, {
                        [styles.active]: isActive,
                        [styles.dragging]: dragging,
                        [styles.reparent]: dropTarget?.mode === 'reparent',
                        [styles.blocked]: blocked,
                        [styles.invalidZone]: inDraggedSubtree,
                    })}
                    style={{marginInlineStart: depth * INDENT_STEP}}
                    onClick={onRowClick}
                >
                    {hasChildren ? (
                        <button
                            type='button'
                            className={styles.chevron}
                            tabIndex={-1}
                            aria-label={toggleLabel}
                            aria-expanded={!isCollapsed}
                            onClick={onChevronClick}
                        >
                            {isCollapsed ? <ChevronRightIcon size={16}/> : <ChevronDownIcon size={16}/>}
                        </button>
                    ) : (
                        <span
                            className={styles.chevronSpacer}
                            aria-hidden={true}
                        />
                    )}
                    <span
                        className={styles.icon}
                        aria-hidden={true}
                    >
                        <PageGlyph size={16}/>
                    </span>
                    {title}
                    {favorited && (
                        <span
                            className={styles.favorite}
                            aria-label={favoriteLabel}
                            role='img'
                        >
                            <StarIcon size={12}/>
                        </span>
                    )}
                    <Spacer/>
                    <PageMenu
                        spaceId={page.space_id}
                        pageId={page.id}
                        pageTitle={page.title}
                        align='right'
                        open={menuOpen}
                        onOpenChange={setMenuOpen}
                        trigger={(
                            <button
                                type='button'
                                className={styles.menuTrigger}
                                tabIndex={-1}
                                aria-label={menuLabel}
                                onClick={(event) => event.stopPropagation()}
                            >
                                <DotsHorizontalIcon size={16}/>
                            </button>
                        )}
                    />
                </div>
                {dropTarget?.mode === 'reorder' && <DropIndicator edge={dropTarget.edge}/>}
            </div>
            {previewContainer && createPortal(
                <PageDragPreview
                    title={page.title}
                    type={page.type}
                    childCount={children.length}
                />,
                previewContainer,
            )}
            {hasChildren && !isCollapsed && (
                <div
                    className={styles.children}
                    role='group'
                >
                    {children.map((child) => (
                        <PageTreeNode
                            key={child.page.id}
                            node={child}
                            activePageId={activePageId}
                            collapsed={collapsed}
                            descendants={descendants}
                            subtreeHeights={subtreeHeights}
                            draggingId={draggingId}
                            dndEnabled={dndEnabled}
                            tabStopId={tabStopId}
                            onSelect={onSelect}
                            onToggle={onToggle}
                            onSetCollapsed={onSetCollapsed}
                            onFocusRow={onFocusRow}
                            onRowKeyDown={onRowKeyDown}
                            registerRow={registerRow}
                        />
                    ))}
                </div>
            )}
        </div>
    );
};

export default PageTreeNode;
