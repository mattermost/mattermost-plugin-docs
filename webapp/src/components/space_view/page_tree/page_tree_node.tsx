// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DropIndicator} from '@atlaskit/pragmatic-drag-and-drop-react-drop-indicator/box';
import classNames from 'classnames';
import {useIsFavorite} from 'hooks/favorites';
import {useTextOverflow} from 'hooks/text_overflow';
import React, {useState} from 'react';
import {createPortal} from 'react-dom';
import {FormattedMessage, useIntl} from 'react-intl';
import {Link} from 'react-router-dom';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ChevronRightIcon from '@mattermost/compass-icons/components/chevron-right';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import FileTextOutlineIcon from '@mattermost/compass-icons/components/file-text-outline';
import FolderOutlineIcon from '@mattermost/compass-icons/components/folder-outline';
import StarIcon from '@mattermost/compass-icons/components/star';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import type {PageNode} from 'store/page_tree';

import PageMenu from 'components/page_menu/page_menu';

import {MAX_PAGE_DEPTH} from './depth';
import {usePageDragDrop} from './dnd/use_page_drag_drop';
import PageDragPreview from './page_drag_preview';
import PageTreeAppendZone from './page_tree_append_zone';
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

    // Where this row points. Rows are real links so a page can be opened in a new
    // tab, copied, or middle-clicked like any other address in the product.
    hrefFor: (node: PageNode) => string;
    onToggle: (pageId: string) => void;
    onSetCollapsed: (ids: string[], collapsed: boolean) => void;
    onFocusRow: (pageId: string) => void;
    onRowKeyDown: (event: React.KeyboardEvent, pageId: string) => void;
    registerRow: (pageId: string, element: HTMLElement | null) => void;
};

const PageTreeNode = ({node, activePageId, collapsed, descendants, subtreeHeights, draggingId, dndEnabled, tabStopId, hrefFor, onToggle, onSetCollapsed, onFocusRow, onRowKeyDown, registerRow}: Props) => {
    const {formatMessage} = useIntl();
    const [element, setElement] = useState<HTMLDivElement | null>(null);
    const [menuOpen, setMenuOpen] = useState(false);
    const [titleOverflowing, titleRef] = useTextOverflow();

    // Narrow subscription: only this page's own favorite state re-renders the row.
    const favorited = useIsFavorite('page', node.id);
    const {id, page, draft, children, depth} = node;
    const spaceId = page?.space_id ?? draft?.space_id ?? '';
    const hasChildren = children.length > 0;
    const isCollapsed = collapsed.has(id);

    // Published rows first, drafts after — kept apart so the append strip can sit
    // between them (see the render below).
    const publishedChildren = children.filter((child) => child.page);
    const draftChildren = children.filter((child) => !child.page);

    // A group only needs a trailing append strip when its last published row is
    // expanded: that row's own bottom edge would be drawn above its children while
    // meaning "after them". Any other last row — a leaf, or a collapsed parent — has
    // nothing below it, so its bottom edge already reads correctly.
    const lastChild = publishedChildren[publishedChildren.length - 1];
    const lastChildExpanded = Boolean(lastChild) && lastChild.children.length > 0 && !collapsed.has(lastChild.id);

    // A drag can never drop into its own subtree; those rows aren't drop targets
    // (truthful canDrop), so mark them as an invalid zone for the whole drag.
    const inDraggedSubtree = draggingId !== null && draggingId !== id && (descendants.get(draggingId)?.has(id) ?? false);
    const isActive = id === activePageId;

    const toggleLabel = isCollapsed ? formatMessage({id: 'docs.pageTree.expand', defaultMessage: 'Expand {title}'}, {title: node.title}) : formatMessage({id: 'docs.pageTree.collapse', defaultMessage: 'Collapse {title}'}, {title: node.title});
    const menuLabel = formatMessage({id: 'docs.pageTree.menu', defaultMessage: 'Page options for {title}'}, {title: node.title});
    const favoriteLabel = formatMessage({id: 'docs.pageTree.favorited', defaultMessage: 'Favorited'});

    const {dragging, dropTarget, blocked, previewContainer} = usePageDragDrop({
        pageId: id,
        element,

        // An unpublished page is neither a drag source nor a drop target: it has no
        // stored order to rewrite, it cannot be a parent, and a published page must
        // not land after it. Disabling the row is what enforces all three — the
        // positions around it simply aren't offered.
        enabled: dndEnabled && Boolean(page),
        expanded: hasChildren && !isCollapsed,
        canDrop: (sourceId, mode) => {
            if (descendants.get(sourceId)?.has(id)) {
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
        const ids = [id, ...(descendants.get(id) ?? [])];
        onSetCollapsed(ids, !isCollapsed);
    };

    // A plain click is left to the link, so modifier-clicks keep their browser
    // meaning (new tab, new window, download). Only the shift-click shortcut is
    // intercepted, since shift on a link means "open in a new window".
    const onLinkClick = (event: React.MouseEvent<HTMLAnchorElement>) => {
        if (event.shiftKey && hasChildren) {
            event.preventDefault();
            toggleRecursive();
        }
    };

    const onChevronClick = (event: React.MouseEvent<HTMLButtonElement>) => {
        event.stopPropagation();
        if (event.shiftKey) {
            toggleRecursive();
            return;
        }
        onToggle(id);
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
        onRowKeyDown(event, id);
    };

    const onFocus = (event: React.FocusEvent<HTMLDivElement>) => {
        if (isOwnEvent(event)) {
            onFocusRow(id);
        }
    };

    const renderChild = (child: PageNode) => (
        <PageTreeNode
            key={child.id}
            node={child}
            activePageId={activePageId}
            collapsed={collapsed}
            descendants={descendants}
            subtreeHeights={subtreeHeights}
            draggingId={draggingId}
            dndEnabled={dndEnabled}
            tabStopId={tabStopId}
            hrefFor={hrefFor}
            onToggle={onToggle}
            onSetCollapsed={onSetCollapsed}
            onFocusRow={onFocusRow}
            onRowKeyDown={onRowKeyDown}
            registerRow={registerRow}
        />
    );

    // Three elements, three jobs: the hit wrapper is the drag/drop target (its box
    // includes the spacing around the row, so a drag between rows still lands in
    // one), the row inside it is what's painted, and the outer treeitem takes focus.
    const setRowElement = (next: HTMLDivElement | null) => setElement(next);

    const setNodeElement = (next: HTMLDivElement | null) => registerRow(id, next);

    const PageGlyph = page?.type === 'page_folder' ? FolderOutlineIcon : FileTextOutlineIcon;

    // Only a clipped title gets a tooltip; a fully visible one would just repeat
    // the text under the pointer. Suppressed for the whole tree during a drag: the
    // pointer is crossing rows to aim at a drop position, and a tooltip popping up
    // over the drop indicator hides the thing the user is aiming with.
    const title = (
        <WithTooltip
            title={node.title}
            disabled={!titleOverflowing || draggingId !== null}
        >
            <span
                ref={titleRef}
                className={styles.title}
            >
                {node.title}
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
            tabIndex={tabStopId === id ? 0 : -1}
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
                    <Link
                        className={styles.rowLink}
                        to={hrefFor(node)}

                        // The row itself is the drag handle; a link's native drag
                        // would start a URL drag and pre-empt it.
                        draggable={false}

                        // The treeitem is the tab stop, so the link stays out of the
                        // tab order and Enter is handled by the row.
                        tabIndex={-1}
                        onClick={onLinkClick}
                    >
                        <span
                            className={styles.icon}
                            aria-hidden={true}
                        >
                            <PageGlyph size={16}/>
                        </span>
                        {title}
                    </Link>
                    {draft && (
                        <span className={styles.draftBadge}>
                            <FormattedMessage
                                id='docs.pageTree.draft'
                                defaultMessage='Draft'
                            />
                        </span>
                    )}
                    {favorited && page && (
                        <span
                            className={styles.favorite}
                            aria-label={favoriteLabel}
                            role='img'
                        >
                            <StarIcon size={12}/>
                        </span>
                    )}
                    <PageMenu
                        spaceId={spaceId}
                        pageId={id}
                        pageTitle={node.title}
                        isDraft={!page}
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
                {/* Indented to the destination's level: the line spans the hit
                    wrapper, which is full width, but the page would land as this
                    row's sibling — so the line starts where those rows start. */}
                {dropTarget?.mode === 'reorder' && (
                    <DropIndicator
                        edge={dropTarget.edge}
                        indent={`${depth * INDENT_STEP}px`}
                    />
                )}
            </div>
            {previewContainer && createPortal(
                <PageDragPreview
                    title={node.title}
                    type={page?.type ?? 'page'}
                    childCount={children.length}
                />,
                previewContainer,
            )}
            {hasChildren && !isCollapsed && (
                <div
                    className={styles.children}
                    role='group'
                >
                    {publishedChildren.map(renderChild)}

                    {/* Between the published rows and the drafts, not after both: a
                        drop here appends to the published run, which is the only
                        place a published page can go. Drawn below the drafts it
                        would point at a position no page can take. */}
                    {lastChildExpanded && (
                        <PageTreeAppendZone
                            parentId={id}
                            indent={(depth + 1) * INDENT_STEP}
                            enabled={dndEnabled}
                            canDrop={(sourceId) => !descendants.get(sourceId)?.has(id) &&
                                sourceId !== id &&
                                depth + 1 + (subtreeHeights.get(sourceId) ?? 0) <= MAX_PAGE_DEPTH}
                        />
                    )}
                    {draftChildren.map(renderChild)}
                </div>
            )}
        </div>
    );
};

export default PageTreeNode;
