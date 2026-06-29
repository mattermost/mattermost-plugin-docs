// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space} from 'types/docs';

export type DocsSearchResults = {
    spaces: Space[];
    pages: Page[];
};

// The seam between the UI/hooks and where Docs data actually comes from. The
// mock source implements this today; an API-backed source (over the Mattermost
// client + plugin REST) replaces it once the server contract exists. Methods
// are synchronous for the mock source and will become Promise-based with the
// real source (hooks would then surface loading/error state).
export interface DocsDataSource {
    getCurrentTeamName(): string;
    listSpaces(): Space[];
    getSpace(id: string): Space | undefined;
    listPages(): Page[];
    getRecentSpaces(): Space[];
    getRecentPages(): Page[];
    search(query: string): DocsSearchResults;
}
