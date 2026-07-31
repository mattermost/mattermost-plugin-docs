// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getPageDraft} from 'client/drafts';
import {getPage} from 'client/pages';
import {RestError} from 'client/rest';
import {useEffect, useState} from 'react';

import type {Page} from 'types/docs';

export type PageDraftLoad = {
    loading: boolean;
    error: unknown;

    title: string;
    body: string;

    page: Page | null;

    fromDraft: boolean;

    notFound: boolean;

    baseEditAt?: number;
};

const initial: PageDraftLoad = {
    loading: true,
    error: null,
    title: '',
    body: '',
    page: null,
    fromDraft: false,
    notFound: false,
};

const isNotFound = (error: unknown): boolean => error instanceof RestError && error.status === 404;

export function usePageDraft(spaceId: string, pageId: string): PageDraftLoad {
    const [state, setState] = useState<PageDraftLoad>(initial);

    useEffect(() => {
        const controller = new AbortController();
        setState(initial);

        const load = async () => {
            const [draftResult, pageResult] = await Promise.allSettled([
                getPageDraft(spaceId, pageId, controller.signal),
                getPage(spaceId, pageId, controller.signal),
            ]);

            if (controller.signal.aborted) {
                return;
            }

            const draft = draftResult.status === 'fulfilled' ? draftResult.value : null;
            const page = pageResult.status === 'fulfilled' ? pageResult.value : null;

            const fatal = [draftResult, pageResult].
                filter((result): result is PromiseRejectedResult => result.status === 'rejected').
                map((result) => result.reason).
                find((reason) => !isNotFound(reason));

            if (fatal) {
                setState({...initial, loading: false, error: fatal});
                return;
            }

            setState({
                loading: false,
                error: null,

                title: draft?.title || page?.title || '',
                body: draft?.body || page?.body || '',
                page,
                fromDraft: Boolean(draft),
                notFound: !draft && !page,
                baseEditAt: page?.edit_at,
            });
        };

        load();

        return () => controller.abort();
    }, [spaceId, pageId]);

    return state;
}
