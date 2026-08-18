// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback} from 'react';
import {useHistory, useLocation} from 'react-router-dom';
import {RHS_QUERY, RHS_VIEW_QUERY} from 'routing/paths';
import {withQuery} from 'routing/query';

export const RHS_IDS = ['info', 'comments'] as const;

export type RhsId = typeof RHS_IDS[number];

export type Rhs = {
    id: RhsId;

    /**
     * The panel's own screen, when it has more than one. Left to the panel to
     * interpret — this hook only carries it in the URL.
     */
    view?: string;
};

const asRhsId = (value: string | null): RhsId | undefined =>
    RHS_IDS.find((id) => id === value);

/**
 * Which right-hand panel is open, from the URL, plus the openers and closers.
 *
 * Every write replaces the history entry instead of pushing one: showing a panel
 * alongside the page is not a move to somewhere else, and pushing would make Back
 * mean "hide the panel" — one Back per open/close, before the user gets out of the
 * page they were reading. Replacing keeps the panel in the URL (so it survives a
 * reload and can be linked) without laying down a trail.
 *
 * An unrecognized `?rhs=` value reads as closed, so a stale or hand-edited URL
 * degrades to the page on its own.
 */
export function useRhs() {
    const history = useHistory();
    const {pathname, search} = useLocation();

    const params = new URLSearchParams(search);
    const id = asRhsId(params.get(RHS_QUERY));
    const view = params.get(RHS_VIEW_QUERY) ?? undefined;
    const rhs: Rhs | null = id ? {id, view} : null;

    const setRhs = useCallback((next: Rhs | null) => {
        history.replace(withQuery(pathname, search, (params) => {
            if (next) {
                params.set(RHS_QUERY, next.id);
            } else {
                params.delete(RHS_QUERY);
            }
            if (next?.view) {
                params.set(RHS_VIEW_QUERY, next.view);
            } else {
                params.delete(RHS_VIEW_QUERY);
            }
        }));
    }, [history, pathname, search]);

    const openRhs = useCallback((nextId: RhsId, nextView?: string) => setRhs({id: nextId, view: nextView}), [setRhs]);

    const closeRhs = useCallback(() => setRhs(null), [setRhs]);

    // Re-pressing the control that opened a panel closes it; pressing another
    // control swaps to that panel, since only one column is on screen at a time.
    const toggleRhs = useCallback((nextId: RhsId) => setRhs(id === nextId ? null : {id: nextId}), [setRhs, id]);

    return {rhs, openRhs, closeRhs, toggleRhs};
}
