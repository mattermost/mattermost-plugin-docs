// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch} from 'hooks/redux';
import {useEffect, useState} from 'react';

import {fetchPage, fetchPageDraft} from 'store/actions';

import type {Page} from 'types/docs';

export type EditorContent = {
    loading: boolean;
    error: unknown;

    title: string;
    body: string;

    page: Page | null;

    fromDraft: boolean;

    notFound: boolean;

    baseEditAt?: number;
};

const initial: EditorContent = {
    loading: true,
    error: null,
    title: '',
    body: '',
    page: null,
    fromDraft: false,
    notFound: false,
};

type Resolved = EditorContent & {key: string};

const keyOf = (spaceId: string, pageId: string): string => `${spaceId}/${pageId}`;

export function useEditorContent(spaceId: string, pageId: string): EditorContent {
    const dispatch = useAppDispatch();
    const [state, setState] = useState<Resolved>(() => ({...initial, key: keyOf(spaceId, pageId)}));

    useEffect(() => {
        const controller = new AbortController();
        const key = keyOf(spaceId, pageId);
        setState({...initial, key});

        const load = async () => {
            let draft;
            let page;
            try {
                [draft, page] = await Promise.all([
                    dispatch(fetchPageDraft(spaceId, pageId, controller.signal)),
                    dispatch(fetchPage(spaceId, pageId)),
                ]);
            } catch (error) {
                if (!controller.signal.aborted) {
                    setState({...initial, key, loading: false, error});
                }
                return;
            }

            if (controller.signal.aborted) {
                return;
            }

            setState({
                key,
                loading: false,
                error: null,

                title: draft?.title || page?.title || '',
                body: draft?.body || page?.body || '',
                page: page ?? null,
                fromDraft: Boolean(draft),
                notFound: !draft && !page,
                baseEditAt: draft?.base_edit_at ?? page?.edit_at,
            });
        };

        load();

        return () => controller.abort();
    }, [dispatch, spaceId, pageId]);

    return state.key === keyOf(spaceId, pageId) ? state : initial;
}
