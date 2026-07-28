// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DropIndicator} from '@atlaskit/pragmatic-drag-and-drop-react-drop-indicator/box';
import classNames from 'classnames';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ChevronRightIcon from '@mattermost/compass-icons/components/chevron-right';
import FileTextOutlineIcon from '@mattermost/compass-icons/components/file-text-outline';
import FolderOutlineIcon from '@mattermost/compass-icons/components/folder-outline';

import type {PageNode} from 'store/page_tree';

import {Button} from 'components/form-controls/button';

import {usePageDragDrop} from './dnd/use_page_drag_drop';
import styles from './page_tree_node.module.scss';

const INDENT_STEP = 16;

type Props = {
    node: PageNode;
    activePageId?: string;
    collapsed: Set<string>;
    descendants: Map<string, Set<string>>;
    dndEnabled: boolean;
    onSelect: (pageId: string) => void;
    onToggle: (pageId: string) => void;
};

const PageTreeNode = ({node, activePageId, collapsed, descendants, dndEnabled, onSelect, onToggle}: Props) => {
    const {formatMessage} = useIntl();
    const [element, setElement] = useState<HTMLDivElement | null>(null);
    const {page, children, depth} = node;
    const hasChildren = children.length > 0;
    const isCollapsed = collapsed.has(page.id);

    const toggleLabel = isCollapsed ?
        formatMessage({id: 'docs.pageTree.expand', defaultMessage: 'Expand {title}'}, {title: page.title}) :
        formatMessage({id: 'docs.pageTree.collapse', defaultMessage: 'Collapse {title}'}, {title: page.title});

    const {dragging, dropTarget} = usePageDragDrop({
        pageId: page.id,
        element,
        enabled: dndEnabled,
        canDrop: (sourceId) => !descendants.get(sourceId)?.has(page.id),
    });

    const PageGlyph = page.type === 'page_folder' ? FolderOutlineIcon : FileTextOutlineIcon;

    return (
        <div className={styles.node}>
            <div
                ref={setElement}
                className={classNames(styles.row, {
                    [styles.active]: page.id === activePageId,
                    [styles.dragging]: dragging,
                    [styles.reparent]: dropTarget?.mode === 'reparent',
                })}
                style={{paddingInlineStart: depth * INDENT_STEP}}
            >
                {hasChildren ? (
                    <Button
                        type='button'
                        emphasis='quaternary'
                        size='sm'
                        className={classNames('btn-icon', styles.chevron)}
                        aria-label={toggleLabel}
                        aria-expanded={!isCollapsed}
                        onClick={() => onToggle(page.id)}
                    >
                        {isCollapsed ? <ChevronRightIcon size={16}/> : <ChevronDownIcon size={16}/>}
                    </Button>
                ) : (
                    <span className={styles.chevronSpacer}/>
                )}
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className={styles.label}
                    title={page.title}
                    onClick={() => onSelect(page.id)}
                >
                    <span
                        className={styles.icon}
                        aria-hidden={true}
                    >
                        <PageGlyph size={16}/>
                    </span>
                    <span className={styles.title}>{page.title}</span>
                </Button>
                {dropTarget?.mode === 'reorder' && (
                    <DropIndicator
                        edge={dropTarget.edge}
                        gap='0px'
                    />
                )}
            </div>
            {hasChildren && !isCollapsed && (
                <div className={styles.children}>
                    {children.map((child) => (
                        <PageTreeNode
                            key={child.page.id}
                            node={child}
                            activePageId={activePageId}
                            collapsed={collapsed}
                            descendants={descendants}
                            dndEnabled={dndEnabled}
                            onSelect={onSelect}
                            onToggle={onToggle}
                        />
                    ))}
                </div>
            )}
        </div>
    );
};

export default PageTreeNode;
