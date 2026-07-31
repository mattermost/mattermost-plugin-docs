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

// Vertical spacing between rows. Shared with the tree's CSS gap so the reorder
// indicator lands in the middle of the gap instead of against a row's edge.
const ROW_GAP = '8px';

type Props = {
    node: PageNode;
    activePageId?: string;
    collapsed: Set<string>;
    descendants: Map<string, Set<string>>;
    subtreeHeights: Map<string, number>;
    draggingId: string | null;
    dndEnabled: boolean;
    onSelect: (pageId: string) => void;
    onToggle: (pageId: string) => void;
    onSetCollapsed: (ids: string[], collapsed: boolean) => void;
};

const PageTreeNode = ({node, activePageId, collapsed, descendants, subtreeHeights, draggingId, dndEnabled, onSelect, onToggle, onSetCollapsed}: Props) => {
    const {formatMessage} = useIntl();
    const [element, setElement] = useState<HTMLDivElement | null>(null);
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

    const onRowKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
        if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            onSelect(page.id);
            return;
        }

        if (!hasChildren) {
            return;
        }

        if ((event.key === 'ArrowRight' && isCollapsed) || (event.key === 'ArrowLeft' && !isCollapsed)) {
            event.preventDefault();
            onToggle(page.id);
        }
    };

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
        <div
            className={styles.node}
            role='none'
        >
            <div
                ref={setElement}
                className={classNames(styles.row, {
                    [styles.active]: isActive,
                    [styles.dragging]: dragging,
                    [styles.reparent]: dropTarget?.mode === 'reparent',
                    [styles.blocked]: blocked,
                    [styles.invalidZone]: inDraggedSubtree,
                })}
                style={{marginInlineStart: depth * INDENT_STEP}}
                role='treeitem'
                tabIndex={0}
                aria-level={depth + 1}
                aria-selected={isActive}
                aria-expanded={hasChildren ? !isCollapsed : undefined}
                onClick={onRowClick}
                onKeyDown={onRowKeyDown}
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
                {dropTarget?.mode === 'reorder' && (
                    <DropIndicator
                        edge={dropTarget.edge}
                        gap={ROW_GAP}
                    />
                )}
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
                    role='none'
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
                            onSelect={onSelect}
                            onToggle={onToggle}
                            onSetCollapsed={onSetCollapsed}
                        />
                    ))}
                </div>
            )}
        </div>
    );
};

export default PageTreeNode;
