// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {CreateSpaceInput, Page, Space} from 'types/docs';

// The seam between the store's thunks and where Docs data actually comes from.
// The mock source implements this today; an API-backed source (over the
// Mattermost client + plugin REST) replaces it once the server contract
// exists. Methods are synchronous for the mock source and will become
// Promise-based with the real source.
export interface DocsDataSource {
    listSpaces(): Space[];
    getSpace(id: string): Space | undefined;

    // Pages belong to a space, so listing them is always scoped to one.
    listPages(spaceId: string): Page[];

    // Creates a space and returns it. Synchronous for the mock source; becomes
    // Promise-based (and may reject) once a real backend exists, which is why
    // the createSpace thunk treats submission as async.
    createSpace(input: CreateSpaceInput): Space;

    // Whether a custom slug is free to use. The mock source checks its
    // in-memory spaces; the real source checks the server.
    isSlugAvailable(slug: string): boolean;
}
