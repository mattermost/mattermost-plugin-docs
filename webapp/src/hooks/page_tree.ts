// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getCollapsed, setCollapsedFor, toggleCollapsed} from 'data/collapsed_pages';
import {useAppSelector} from 'hooks/redux';
import {useCallback, useState} from 'react';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

type CollapsedPages = {
    collapsed: Set<string>;
    toggle: (pageId: string) => void;
    setCollapsedFor: (ids: string[], collapsed: boolean) => void;
};

// Owns the current user's collapsed-node set for the page tree, backed by
// localStorage (see data/collapsed_pages). Toggling persists and re-renders.
export function useCollapsedPages(): CollapsedPages {
    const userId = useAppSelector(getCurrentUserId);
    const [collapsed, setCollapsed] = useState<Set<string>>(() => getCollapsed(userId));

    const toggle = useCallback((pageId: string) => {
        setCollapsed(toggleCollapsed(userId, pageId));
    }, [userId]);

    const setMany = useCallback((ids: string[], next: boolean) => {
        setCollapsed(setCollapsedFor(userId, ids, next));
    }, [userId]);

    return {collapsed, toggle, setCollapsedFor: setMany};
}
