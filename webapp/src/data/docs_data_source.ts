// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {CreateSpaceInput, Page, Space, SpaceSummary} from 'types/docs';

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
    listSpaces(): Space[];
    getSpace(id: string): Space | undefined;

    // Pages belong to a space, so listing them is always scoped to one.
    listPages(spaceId: string): Page[];
    getRecentSpaces(): Space[];
    getRecentSpaceSummaries(): SpaceSummary[];
    getRecentPages(): Page[];
    search(query: string): DocsSearchResults;

    // Creates a space and returns it. Synchronous for the mock source; becomes
    // Promise-based (and may reject) once a real backend exists, which is why
    // the hook treats submission as async.
    createSpace(input: CreateSpaceInput): Space;

    // Whether a custom slug is free to use. A no-op (always true) for the mock
    // source — the real source checks the server. Consumers treat the result as
    // async (the hook awaits it) so the swap needs no UI change.
    isSlugAvailable(slug: string): boolean;
}
