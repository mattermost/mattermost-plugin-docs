// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useState} from 'react';
import {useIntl} from 'react-intl';

import {createDraft, fetchPage} from 'store/actions';
import {getPage, getPageInSpace} from 'store/selectors';

import {toast} from 'components/toast';

import {SPACE_PROP_DEFAULT_PAGE_ID, UNTITLED_PAGE_TITLE} from 'types/docs';
import type {Page, Space} from 'types/docs';

import {useDocsNavigation} from './navigation';
import {useAppDispatch, useAppSelector} from './redux';

/**
 * Returns a handler that starts a new top-level page in the space and opens it for
 * writing. Shared by the page header's add button and the page tree's "Add page".
 *
 * It creates a *draft*, not a page: nobody else sees a half-written page, and the
 * author decides when it becomes real by publishing. The draft reserves the page id
 * up front, which is what makes it addressable at `/drafts/:pageId` before it
 * exists as a page.
 *
 * The draft carries the untitled placeholder as its title because publishing
 * requires a non-empty one; the title field renders that as empty (see PageTitle).
 */
export function useCreateRootPage(spaceId: string) {
    const dispatch = useAppDispatch();
    const {goToEditDraft} = useDocsNavigation();
    const {formatMessage} = useIntl();
    const untitled = formatMessage(UNTITLED_PAGE_TITLE);

    return useCallback(async () => {
        try {
            const draft = await dispatch(createDraft(spaceId, untitled));
            goToEditDraft(spaceId, draft.page_id);
        } catch (error) {
            // Without this the add-page buttons look inert on failure.
            toast.error(formatMessage({id: 'docs.pageTree.addFailed', defaultMessage: 'Could not create the page. Please try again.'}));

            // eslint-disable-next-line no-console
            console.error('Docs: failed to create page', error);
        }
    }, [dispatch, spaceId, untitled, goToEditDraft, formatMessage]);
}

export type RoutedPage = {
    page?: Page;

    // False until the id has an answer — see RoutedSpace.resolved.
    resolved: boolean;
};

/**
 * Resolves a page id that came from the URL against the space, fetching it by id
 * when the space's page list doesn't hold it.
 *
 * The list is the usual source, but it can predate the page, so an id missing from
 * it is not yet a bad id. Fetching by id is also what supplies the page's body — the
 * list returns summaries.
 *
 * `fetchMissing` is false on the draft route, where the page legitimately does not
 * exist (a draft reserves its page id before publishing) and asking for it would be
 * a 404 on every draft opened.
 */
export function useRoutedPage(spaceId: string, pageId?: string, {fetchMissing = true} = {}): RoutedPage {
    const dispatch = useAppDispatch();
    const page = useAppSelector((state) => (pageId ? getPageInSpace(state, spaceId, pageId) : undefined));
    const [checkedId, setCheckedId] = useState<string>();
    const missing = Boolean(pageId) && !page;

    useEffect(() => {
        if (!pageId || !missing || !fetchMissing) {
            return undefined;
        }
        let active = true;
        dispatch(fetchPage(spaceId, pageId)).
            catch(() => undefined).
            finally(() => {
                if (active) {
                    setCheckedId(pageId);
                }
            });
        return () => {
            active = false;
        };
    }, [dispatch, spaceId, pageId, missing, fetchMissing]);

    return {page, resolved: Boolean(pageId) && (Boolean(page) || checkedId === pageId)};
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
