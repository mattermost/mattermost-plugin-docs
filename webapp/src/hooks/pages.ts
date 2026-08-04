// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback} from 'react';
import {useIntl} from 'react-intl';

import {createPage} from 'store/actions';
import {getPage} from 'store/selectors';

import {toast} from 'components/toast';

import {SPACE_PROP_DEFAULT_PAGE_ID} from 'types/docs';
import type {Space} from 'types/docs';

import {useDocsNavigation} from './navigation';
import {useAppDispatch, useAppSelector} from './redux';

/**
 * Returns a handler that creates a top-level page in the space and opens it in
 * edit mode. Shared by the page header's add button and the page tree's "Add
 * page". A new page is empty and titled "Untitled", so reading it is never what
 * was wanted; edit mode also focuses the title, which is the first thing to fill
 * in.
 */
export function useCreateRootPage(spaceId: string) {
    const dispatch = useAppDispatch();
    const {goToEditPage} = useDocsNavigation();
    const {formatMessage} = useIntl();
    const untitled = formatMessage({id: 'docs.pageTree.untitled', defaultMessage: 'Untitled'});

    return useCallback(async () => {
        try {
            const page = await dispatch(createPage(spaceId, {title: untitled}));
            goToEditPage(spaceId, page.id, {replace: true});
        } catch (error) {
            // Without this the add-page buttons look inert on failure.
            toast.error(formatMessage({id: 'docs.pageTree.addFailed', defaultMessage: 'Could not create the page. Please try again.'}));

            // eslint-disable-next-line no-console
            console.error('Docs: failed to create page', error);
        }
    }, [dispatch, spaceId, untitled, goToEditPage, formatMessage]);
}

/**
 * The path a space-home URL should redirect to when the space has a default page
 * configured, or undefined when it should stay on the space front door. Render
 * the result through react-router's `<Redirect>`, which replaces the history
 * entry so Back doesn't bounce straight into the redirect again.
 *
 * Resolves the page from the store rather than trusting the prop: pages load
 * asynchronously, and the prop can still name a page that was since deleted or
 * moved to another space.
 */
export function useDefaultPagePath(space: Space): string | undefined {
    const {pageId, isOverview, paths} = useDocsNavigation();

    const defaultPageId = space.props?.[SPACE_PROP_DEFAULT_PAGE_ID] ?? '';
    const defaultPage = useAppSelector((state) => (defaultPageId ? getPage(state, defaultPageId) : undefined));

    // An explicit /overview request outranks the default page; otherwise the
    // space's front door would be unreachable once a default is set.
    if (pageId || isOverview || !defaultPage || defaultPage.space_id !== space.id) {
        return undefined;
    }
    return paths.page(space.id, defaultPage.id);
}
