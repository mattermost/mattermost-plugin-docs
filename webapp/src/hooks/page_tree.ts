// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getCollapsed, setCollapsedFor, toggleCollapsed} from 'data/collapsed_pages';
import {useAppSelector} from 'hooks/redux';
import {useCallback, useSyncExternalStore} from 'react';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

// Like the sidebar widths (hooks/sidebar_width), the collapsed set lives in a
// module-level store rather than component state: it's per-user UI state backed by
// localStorage, and every reader has to see the same live value. Component state
// would let a second consumer drift out of sync with the tree's own toggles.
const collapsedByUser = new Map<string, Set<string>>();
const listeners = new Set<() => void>();

const subscribe = (listener: () => void): (() => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

const publish = (userId: string, next: Set<string>): void => {
    collapsedByUser.set(userId, next);
    listeners.forEach((listener) => listener());
};

// Idempotent: memoizes the initial localStorage read so getSnapshot stays stable
// (a fresh Set every call would re-render forever).
const current = (userId: string): Set<string> => {
    const known = collapsedByUser.get(userId);
    if (known) {
        return known;
    }
    const initial = getCollapsed(userId);
    collapsedByUser.set(userId, initial);
    return initial;
};

type CollapsedPages = {
    collapsed: Set<string>;
    toggle: (pageId: string) => void;
    setCollapsedFor: (ids: string[], collapsed: boolean) => void;
};

/**
 * The current user's collapsed-node set for the page tree, persisted to
 * localStorage (see data/collapsed_pages). Toggling persists and re-renders every
 * consumer.
 */
export function useCollapsedPages(): CollapsedPages {
    const userId = useAppSelector(getCurrentUserId);

    const collapsed = useSyncExternalStore(subscribe, () => current(userId));

    const toggle = useCallback((pageId: string) => {
        publish(userId, toggleCollapsed(userId, pageId));
    }, [userId]);

    const setMany = useCallback((ids: string[], next: boolean) => {
        publish(userId, setCollapsedFor(userId, ids, next));
    }, [userId]);

    return {collapsed, toggle, setCollapsedFor: setMany};
}
