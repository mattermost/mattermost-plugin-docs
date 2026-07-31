// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useCollapsedPages} from 'hooks/page_tree';
import {useCreateRootPage} from 'hooks/pages';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import React, {useCallback, useMemo} from 'react';
import {FormattedMessage} from 'react-intl';

import PlusIcon from '@mattermost/compass-icons/components/plus';

import {movePage} from 'store/actions';
import {buildDescendantMap, buildPageTree} from 'store/page_tree';
import {getPagesForSpace} from 'store/selectors';

import {Button} from 'components/form_controls/button';

import type {Space} from 'types/docs';

import {buildSubtreeHeightMap} from './depth';
import {usePagesDnd} from './dnd/use_pages_dnd';
import PageTreeNode from './page_tree_node';
import styles from './page_tree_panel.module.scss';
import SpaceHomeItem from './space_home_item';

import {PAGES_SECTION_LABEL_ID} from '../page_header';

const PageTreePanel = ({space}: {space: Space}) => {
    const dispatch = useAppDispatch();
    const {goToPage, pageId} = useDocsNavigation();
    const {collapsed, toggle, setCollapsedFor} = useCollapsedPages();
    const createRootPage = useCreateRootPage(space.id);

    const pages = useAppSelector((state) => getPagesForSpace(state, space.id));
    const roots = useMemo(() => buildPageTree(pages), [pages]);
    const descendants = useMemo(() => buildDescendantMap(roots), [roots]);
    const subtreeHeights = useMemo(() => buildSubtreeHeightMap(roots), [roots]);

    const onMove = useCallback(({pageId: movedId, parentId, siblingIndex}: {pageId: string; parentId: string; siblingIndex: number}) => {
        dispatch(movePage(space.id, movedId, parentId, siblingIndex));
    }, [dispatch, space.id]);

    const {draggingId} = usePagesDnd({pages, onMove, enabled: pages.length > 0});

    return (
        <div className={styles.panel}>
            <SpaceHomeItem space={space}/>

            <div
                className={styles.tree}
                role='tree'
                aria-labelledby={PAGES_SECTION_LABEL_ID}
            >
                {roots.map((node) => (
                    <PageTreeNode
                        key={node.page.id}
                        node={node}
                        activePageId={pageId}
                        collapsed={collapsed}
                        descendants={descendants}
                        subtreeHeights={subtreeHeights}
                        draggingId={draggingId}
                        dndEnabled={true}
                        onSelect={(id) => goToPage(space.id, id)}
                        onToggle={toggle}
                        onSetCollapsed={setCollapsedFor}
                    />
                ))}
            </div>

            <div
                className={styles.separator}
                role='presentation'
            />
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
        </div>
    );
};

export default PageTreePanel;
