// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getSpaceViews} from 'data/recent_spaces';
import {useAppSelector} from 'hooks/redux';
import {useMemo} from 'react';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {getSpacesById, searchDocs} from 'store/selectors';
import type {DocsSearchResults} from 'store/selectors';

import type {Page, Space} from 'types/docs';

const EMPTY_PAGES: Page[] = [];

// Recently-viewed docs for the switcher. Cross-team recent spaces resolved from
// the client-side recency store; recent pages await the page tree (Pages later).
export function useRecentDocs(): {spaces: Space[]; pages: Page[]} {
    const userId = useAppSelector(getCurrentUserId);
    const spacesById = useAppSelector(getSpacesById);
    return useMemo(() => {
        const spaces = getSpaceViews(userId).flatMap(({spaceId}) => {
            const space = spacesById[spaceId];
            return space ? [space] : [];
        });
        return {spaces, pages: EMPTY_PAGES};
    }, [userId, spacesById]);
}

// Filtering lives in the store selector, not the UI; later this can debounce
// against a server search endpoint.
export function useDocsSearch(query: string): DocsSearchResults {
    return useAppSelector((state) => searchDocs(state, query));
}
