// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppSelector} from 'hooks/redux';
import {useMemo} from 'react';

import {getRecentPages, getRecentSpaces, searchDocs} from 'store/selectors';
import type {DocsSearchResults} from 'store/selectors';

import type {Page, Space} from 'types/docs';

export function useRecentDocs(): {spaces: Space[]; pages: Page[]} {
    const spaces = useAppSelector(getRecentSpaces);
    const pages = useAppSelector(getRecentPages);
    return useMemo(() => ({spaces, pages}), [spaces, pages]);
}

// Filtering lives in the store selector, not the UI; later this can debounce
// against a server search endpoint.
export function useDocsSearch(query: string): DocsSearchResults {
    return useAppSelector((state) => searchDocs(state, query));
}
