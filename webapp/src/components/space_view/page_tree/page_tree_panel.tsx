// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useCollapsedPages} from 'hooks/page_tree';
import {useCreateRootPage} from 'hooks/pages';
import {useCanCreatePage} from 'hooks/permissions';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import React, {useCallback, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useHistory} from 'react-router-dom';

import PlusIcon from '@mattermost/compass-icons/components/plus';

import {movePage} from 'store/actions';
import {buildDescendantMap, buildPageTree} from 'store/page_tree';
import type {PageNode} from 'store/page_tree';
import {getOrphanDraftsForSpace, getPagesForSpace} from 'store/selectors';

import {Button} from 'components/form_controls/button';
import {announce} from 'components/readout';
import {toast} from 'components/toast';

import type {Space} from 'types/docs';

import {MAX_PAGE_DEPTH, MAX_PAGE_LEVELS, buildSubtreeHeightMap} from './depth';
import {usePagesDnd} from './dnd/use_pages_dnd';
import {flattenVisibleRows, resolveReorder} from './keyboard';
import type {ReorderIntent} from './keyboard';
import PageTreeAppendZone from './page_tree_append_zone';
import PageTreeNode from './page_tree_node';
import styles from './page_tree_panel.module.scss';
import SpaceHomeItem from './space_home_item';

import {PAGES_KEYBOARD_HELP_ID, PAGES_SECTION_LABEL_ID} from '../page_header';

const REORDER_KEYS: Record<string, ReorderIntent> = {
    ArrowUp: 'up',
    ArrowDown: 'down',
    ArrowRight: 'indent',
    ArrowLeft: 'outdent',
};

const PageTreePanel = ({space}: {space: Space}) => {
    const dispatch = useAppDispatch();
    const {formatMessage} = useIntl();
    const {pageId, paths} = useDocsNavigation();
    const history = useHistory();
    const {collapsed, toggle, setCollapsedFor} = useCollapsedPages();
    const createRootPage = useCreateRootPage(space.id);
    const canCreatePage = useCanCreatePage(space.id);

    const pages = useAppSelector((state) => getPagesForSpace(state, space.id));

    // Only drafts with no published page get a row. Unpublished edits to a page
    // already in the tree belong on that page's own row, not on a second one.
    const orphanDrafts = useAppSelector((state) => getOrphanDraftsForSpace(state, space.id));
    const roots = useMemo(() => buildPageTree(pages, orphanDrafts), [pages, orphanDrafts]);
    const descendants = useMemo(() => buildDescendantMap(roots), [roots]);
    const subtreeHeights = useMemo(() => buildSubtreeHeightMap(roots), [roots]);
    const visibleRows = useMemo(() => flattenVisibleRows(roots, collapsed), [roots, collapsed]);

    // A draft has no published page, so it is addressed as a draft. One helper for
    // the row links and for the keyboard, so the two can never disagree.
    const hrefFor = useCallback(
        (node: PageNode) => (node.page ? paths.page(space.id, node.id) : paths.draft(space.id, node.id)),
        [paths, space.id],
    );

    // Published rows first, drafts after; split so the root append strip lands
    // between them rather than below the drafts (see the render).
    const publishedRoots = roots.filter((node) => node.page);
    const draftRoots = roots.filter((node) => !node.page);

    // See the trailing append strip below: the root group needs one only when its
    // last published row is expanded, and so has children rendered beneath its
    // bottom edge.
    const lastRoot = publishedRoots[publishedRoots.length - 1];
    const lastRootExpanded = Boolean(lastRoot) && lastRoot.children.length > 0 && !collapsed.has(lastRoot.id);

    // The tree is a single tab stop (roving tabindex), so it tracks which row owns
    // it. Focus itself moves imperatively through the registry below rather than
    // through a prop, so an arrow key re-renders two rows instead of the tree.
    const [focusedId, setFocusedId] = useState<string | null>(null);
    const rowElements = useRef(new Map<string, HTMLElement>());

    const registerRow = useCallback((id: string, element: HTMLElement | null) => {
        if (element) {
            rowElements.current.set(id, element);
        } else {
            rowElements.current.delete(id);
        }
    }, []);

    const focusRow = useCallback((id: string) => {
        setFocusedId(id);
        rowElements.current.get(id)?.focus();
    }, []);

    // Whether focus is (or was last) inside the tree. Set on row focus; cleared only
    // when focus moves to something outside, not when it's simply lost.
    const treeHasFocus = useRef(false);

    const onFocusRow = useCallback((id: string) => {
        treeHasFocus.current = true;
        setFocusedId(id);
    }, []);

    const onTreeBlur = useCallback((event: React.FocusEvent) => {
        const next = event.relatedTarget as Node | null;
        if (next && !event.currentTarget.contains(next)) {
            treeHasFocus.current = false;
        }
    }, []);

    const focusIndex = useRef(-1);

    // Re-parenting a row (nest/unnest, or the revert when the server refuses the
    // move) remounts it, and deleting a page removes it outright — both drop DOM
    // focus to the body. Rather than arming a one-shot before each of those, this
    // holds the invariant: if the tree owned focus and no longer has it, take it
    // back. It only ever reclaims focus the tree just lost, never steals it.
    // Layout effect so there's no painted frame without a focus ring.
    useLayoutEffect(() => {
        const index = visibleRows.findIndex((row) => row.node.id === focusedId);
        if (index !== -1) {
            focusIndex.current = index;
        }

        const active = document.activeElement;
        const lost = active === null || active === document.body;
        if (!treeHasFocus.current || !lost || !focusedId || visibleRows.length === 0) {
            return;
        }

        // The focused row survived, or it's gone and the row that took its place
        // (clamped to the end) inherits focus.
        const target = index === -1 ? visibleRows[Math.min(focusIndex.current, visibleRows.length - 1)] : visibleRows[index];
        if (!target) {
            return;
        }

        const targetId = target.node.id;
        setFocusedId(targetId);
        rowElements.current.get(targetId)?.focus();
    }, [visibleRows, focusedId]);

    // Covers both drag-and-drop and the keyboard reorder below. movePage rejects
    // after restoring server truth, so a silent snap-back gets an explanation.
    const onMove = useCallback(({pageId: movedId, parentId, siblingIndex}: {pageId: string; parentId: string; siblingIndex: number}) => {
        dispatch(movePage(space.id, movedId, parentId, siblingIndex)).catch(() => {
            toast.error(formatMessage({id: 'docs.pageTree.moveFailed', defaultMessage: 'Could not move the page. Please try again.'}));
        });
    }, [dispatch, space.id, formatMessage]);

    const {draggingId} = usePagesDnd({pages, onMove, enabled: pages.length > 0});

    // Drag-and-drop can't be the only way to reorder (WCAG 2.1.1), so Alt+arrows
    // perform the same four moves and announce the outcome.
    const reorder = useCallback((movedId: string, intent: ReorderIntent) => {
        const {move, blocked} = resolveReorder(roots, subtreeHeights, movedId, intent);
        const title = pages.find((page) => page.id === movedId)?.title ?? '';

        if (!move) {
            announce(blocked === 'depth' ? formatMessage({
                id: 'docs.pageTree.nestBlocked',
                defaultMessage: 'Cannot nest {title} deeper than {levels} levels.',
            }, {title, levels: MAX_PAGE_LEVELS}) : formatMessage({
                id: 'docs.pageTree.moveBlocked',
                defaultMessage: 'Cannot move {title} any further in that direction.',
            }, {title}));
            return;
        }

        onMove(move);

        // Announced optimistically, to match the row that has already visibly moved
        // — waiting for the server would put the announcement behind the change it
        // describes. A rejected move is corrected by the error toast, which Base UI
        // renders in its own live region.
        announce(formatMessage({
            id: 'docs.pageTree.moved',
            defaultMessage: 'Moved {title} to position {position} under {parent}.',
        }, {
            title,
            position: move.siblingIndex + 1,
            parent: pages.find((page) => page.id === move.parentId)?.title ??
                formatMessage({id: 'docs.pageTree.topLevel', defaultMessage: 'top level'}),
        }));
    }, [roots, subtreeHeights, pages, onMove, formatMessage]);

    // One handler for the whole tree: the panel is what knows the visible order,
    // so a row would only have to hand these decisions back anyway.
    const onRowKeyDown = useCallback((event: React.KeyboardEvent, rowId: string) => {
        if (event.currentTarget !== event.target) {
            return;
        }

        const index = visibleRows.findIndex((row) => row.node.id === rowId);
        if (index === -1) {
            return;
        }

        const {node, parentId} = visibleRows[index];
        const hasChildren = node.children.length > 0;
        const isCollapsed = collapsed.has(rowId);

        const reorderIntent = event.altKey ? REORDER_KEYS[event.key] : undefined;
        if (reorderIntent) {
            event.preventDefault();
            reorder(rowId, reorderIntent);
            return;
        }

        switch (event.key) {
        case 'ArrowDown':
            if (index < visibleRows.length - 1) {
                focusRow(visibleRows[index + 1].node.id);
            }
            break;
        case 'ArrowUp':
            if (index > 0) {
                focusRow(visibleRows[index - 1].node.id);
            }
            break;
        case 'Home':
            focusRow(visibleRows[0].node.id);
            break;
        case 'End':
            focusRow(visibleRows[visibleRows.length - 1].node.id);
            break;

        // Expand, then step into the subtree — the first child is the next
        // visible row once this node is open.
        case 'ArrowRight':
            if (hasChildren && isCollapsed) {
                toggle(rowId);
            } else if (hasChildren) {
                focusRow(visibleRows[index + 1].node.id);
            }
            break;

        // Collapse, or climb to the parent when there's nothing to close.
        case 'ArrowLeft':
            if (hasChildren && !isCollapsed) {
                toggle(rowId);
            } else if (parentId) {
                focusRow(parentId);
            }
            break;
        case 'Enter':
        case ' ':
            history.push(hrefFor(node));
            break;
        default:
            return;
        }

        event.preventDefault();
    }, [visibleRows, collapsed, reorder, focusRow, toggle, history, hrefFor]);

    // The row that owns the tab stop: wherever focus last was, else the routed
    // page, else the first row — so Tab always lands somewhere sensible.
    const tabStopId = useMemo(() => {
        const isVisible = (id: string | null | undefined) => Boolean(id) && visibleRows.some((row) => row.node.id === id);
        if (focusedId && isVisible(focusedId)) {
            return focusedId;
        }
        if (isVisible(pageId)) {
            return pageId;
        }
        return visibleRows[0]?.node.id;
    }, [focusedId, pageId, visibleRows]);

    const renderRoot = (node: PageNode) => (
        <PageTreeNode
            key={node.id}
            node={node}
            activePageId={pageId}
            collapsed={collapsed}
            descendants={descendants}
            subtreeHeights={subtreeHeights}
            draggingId={draggingId}
            dndEnabled={true}
            tabStopId={tabStopId}
            hrefFor={hrefFor}
            onToggle={toggle}
            onSetCollapsed={setCollapsedFor}
            onFocusRow={onFocusRow}
            onRowKeyDown={onRowKeyDown}
            registerRow={registerRow}
        />
    );

    return (
        <div className={styles.panel}>
            <SpaceHomeItem space={space}/>

            <div
                className={styles.tree}
                role='tree'
                aria-labelledby={PAGES_SECTION_LABEL_ID}
                aria-describedby={PAGES_KEYBOARD_HELP_ID}
                onBlur={onTreeBlur}
            >
                {publishedRoots.map(renderRoot)}

                {/* Only when the last published root is expanded: otherwise its own
                    bottom edge already means "last at root" and is drawn in the right
                    place. Appending at root puts the moved page at depth 0, so only
                    its own subtree has to fit under the cap. Placed before the draft
                    rows, since that is where an appended page actually lands. */}
                {lastRootExpanded && (
                    <PageTreeAppendZone
                        parentId=''
                        indent={0}
                        enabled={true}
                        canDrop={(sourceId) => (subtreeHeights.get(sourceId) ?? 0) <= MAX_PAGE_DEPTH}
                    />
                )}
                {draftRoots.map(renderRoot)}
            </div>

            <p
                id={PAGES_KEYBOARD_HELP_ID}
                className={styles.visuallyHidden}
            >
                <FormattedMessage
                    id='docs.pageTree.keyboardHelp'
                    defaultMessage='Use the arrow keys to move through pages. Hold Alt with an arrow key to move the selected page up, down, in, or out.'
                />
            </p>

            <div
                className={styles.separator}
                role='presentation'
            />
            {canCreatePage && (
                <Button
                    emphasis='quaternary'
                    size='sm'
                    className={styles.addPage}
                    leadingIcon={<PlusIcon size={16}/>}
                    onClick={createRootPage}
                >
                    <FormattedMessage
                        id='docs.pageTree.add'
                        defaultMessage='Add page'
                    />
                </Button>
            )}
        </div>
    );
};

export default PageTreePanel;
