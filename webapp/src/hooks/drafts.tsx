// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PublishConflictError} from 'data/publish_conflict';
import {useCallback, useEffect, useState} from 'react';
import {useIntl} from 'react-intl';

import {fetchPageDraft, publishDraft} from 'store/actions';
import {getDraftForPage} from 'store/selectors';

import {toast} from 'components/toast';

import {ConflictReason} from 'types/drafts';
import type {Draft} from 'types/drafts';

import {useDocsNavigation} from './navigation';
import {useAppDispatch, useAppSelector} from './redux';

type LoadedDraft = {
    draft: Draft | undefined;

    // False until the fetch settles. A draft URL that names nothing must not be
    // corrected before the answer is in, or a slow response looks like a bad URL.
    loaded: boolean;
};

/**
 * Loads the caller's draft for a page and returns it from the store.
 *
 * Pass undefined ids to skip the load. The request is aborted if the routed page
 * changes mid-flight, so a slow response for the previous page can't land after the
 * new one — the abort rejects with AbortError, which is not a failure to report.
 */
export function usePageDraft(spaceId?: string, pageId?: string): LoadedDraft {
    const dispatch = useAppDispatch();
    const draft = useAppSelector((state) => (pageId ? getDraftForPage(state, pageId) : undefined));
    const [loadedId, setLoadedId] = useState<string>();

    useEffect(() => {
        if (!spaceId || !pageId) {
            return undefined;
        }
        const controller = new AbortController();

        dispatch(fetchPageDraft(spaceId, pageId, controller.signal)).
            then(() => setLoadedId(pageId)).
            catch((error) => {
                if (controller.signal.aborted) {
                    return;
                }

                // Settled either way: the caller decides what a missing draft means,
                // and retrying on its own would just fail again.
                setLoadedId(pageId);

                // eslint-disable-next-line no-console
                console.error('Docs: failed to load draft', error);
            });

        return () => controller.abort();
    }, [dispatch, spaceId, pageId]);

    return {draft, loaded: Boolean(pageId) && loadedId === pageId};
}

/**
 * Returns a handler that publishes a draft into its page and routes to that page.
 *
 * The arrival replaces: publishing destroys the draft, so its URL is dead and Back
 * must not return to it.
 */
export function usePublishDraft(spaceId: string) {
    const dispatch = useAppDispatch();
    const {formatMessage} = useIntl();
    const {goToPage} = useDocsNavigation();

    return useCallback(async (pageId: string) => {
        try {
            const page = await dispatch(publishDraft(spaceId, pageId));
            goToPage(spaceId, page.id, {replace: true});
        } catch (error) {
            // A parent that isn't published yet is the one conflict force can't
            // resolve, so it gets the instruction that actually helps instead of a
            // generic failure.
            if (error instanceof PublishConflictError && error.reason === ConflictReason.ParentUnpublished) {
                toast.error(formatMessage({
                    id: 'docs.publish.parentUnpublished',
                    defaultMessage: 'Publish the parent page first, then publish this one.',
                }));
                return;
            }
            toast.error(formatMessage({
                id: 'docs.publish.failed',
                defaultMessage: 'Could not publish the page. Please try again.',
            }));

            // eslint-disable-next-line no-console
            console.error('Docs: failed to publish draft', error);
        }
    }, [dispatch, spaceId, goToPage, formatMessage]);
}
