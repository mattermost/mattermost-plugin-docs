// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';
import type {DocsSearchResults} from 'data';
import {useMemo} from 'react';

import type {Page, Space} from 'types/docs';

export function useRecentDocs(): {spaces: Space[]; pages: Page[]} {
    return useMemo(() => ({
        spaces: docsDataSource.getRecentSpaces(),
        pages: docsDataSource.getRecentPages(),
    }), []);
}

// Filtering lives in the data layer, not the UI; later this can debounce
// against a server search endpoint.
export function useDocsSearch(query: string): DocsSearchResults {
    return useMemo(() => docsDataSource.search(query), [query]);
}
