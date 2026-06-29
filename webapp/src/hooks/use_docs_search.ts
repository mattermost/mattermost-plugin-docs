// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';
import type {DocsSearchResults} from 'data';
import {useMemo} from 'react';

// Spaces and pages matching the query. Filtering lives in the data layer, not
// the UI; later this can debounce against a server search endpoint.
export function useDocsSearch(query: string): DocsSearchResults {
    return useMemo(() => docsDataSource.search(query), [query]);
}
