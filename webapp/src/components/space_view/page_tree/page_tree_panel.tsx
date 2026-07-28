// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useCollapsedPages} from 'hooks/page_tree';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import React, {useCallback, useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import PlusIcon from '@mattermost/compass-icons/components/plus';

import {createPage, movePage} from 'store/actions';
import {buildDescendantMap, buildPageTree} from 'store/page_tree';
import {getPagesForSpace} from 'store/selectors';

import {Button} from 'components/form_controls/button';

import type {Space} from 'types/docs';

import {usePagesDnd} from './dnd/use_pages_dnd';
import PageTreeNode from './page_tree_node';
import styles from './page_tree_panel.module.scss';

const PageTreePanel = ({space}: {space: Space}) => {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();
    const {goToPage, pageId} = useDocsNavigation();
    const {collapsed, toggle} = useCollapsedPages();

    const pages = useAppSelector((state) => getPagesForSpace(state, space.id));
    const roots = useMemo(() => buildPageTree(pages), [pages]);
    const descendants = useMemo(() => buildDescendantMap(roots), [roots]);

    const untitled = formatMessage({id: 'docs.pageTree.untitled', defaultMessage: 'Untitled'});
    const addLabel = formatMessage({id: 'docs.pageTree.add', defaultMessage: 'Add page'});

    const createRootPage = useCallback(async () => {
        try {
            const page = await dispatch(createPage(space.id, {title: untitled}));
            goToPage(space.id, page.id);
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Docs: failed to create page', error);
        }
    }, [dispatch, space.id, untitled, goToPage]);

    const onMove = useCallback(({pageId: movedId, parentId, siblingIndex}: {pageId: string; parentId: string; siblingIndex: number}) => {
        dispatch(movePage(space.id, movedId, parentId, siblingIndex));
    }, [dispatch, space.id]);

    usePagesDnd({pages, onMove, enabled: pages.length > 0});

    return (
        <div className={styles.panel}>
            <div className={styles.header}>
                <span className={styles.heading}>
                    <FormattedMessage
                        id='docs.pageTree.heading'
                        defaultMessage='Pages'
                    />
                </span>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className='btn-icon'
                    aria-label={addLabel}
                    onClick={createRootPage}
                >
                    <PlusIcon size={16}/>
                </Button>
            </div>

            <div className={styles.tree}>
                {roots.map((node) => (
                    <PageTreeNode
                        key={node.page.id}
                        node={node}
                        activePageId={pageId}
                        collapsed={collapsed}
                        descendants={descendants}
                        dndEnabled={true}
                        onSelect={(id) => goToPage(space.id, id)}
                        onToggle={toggle}
                    />
                ))}
            </div>

            <Button
                type='button'
                emphasis='quaternary'
                size='sm'
                className={styles.addPage}
                onClick={createRootPage}
            >
                <PlusIcon size={16}/>
                <FormattedMessage
                    id='docs.pageTree.add'
                    defaultMessage='Add page'
                />
            </Button>
        </div>
    );
};

export default PageTreePanel;
