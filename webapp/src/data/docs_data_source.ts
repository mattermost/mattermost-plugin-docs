// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {CreateSpaceInput, Page, Space} from 'types/docs';

// The seam between the store's thunks and the Docs server REST API. The
// API-backed source implements this over the plugin's /api/v1 routes; the
// interface stays transport-agnostic so tests can substitute a fake.
//
// All methods are async: they map to network calls. Ids are the platform's
// opaque 26-char ids (no slugs) and space reads/lists are team-scoped, matching
// the server contract.
export interface DocsDataSource {

    // Spaces the caller is a member of in the given team (the server filters by
    // backing-channel membership).
    listSpaces(teamId: string): Promise<Space[]>;

    getSpace(spaceId: string): Promise<Space | undefined>;

    // Creates a space in the team and returns it (with its server-assigned id
    // and team_id). Only server-supported fields are sent; client-only fields
    // (slug, visibility) are dropped by the API source until the server models
    // them (see PR #10's view_access).
    createSpace(teamId: string, input: CreateSpaceInput): Promise<Space>;

    // Removes a member from a space. Leaving a space is removing yourself; the
    // server rejects removing the last authorized member (409).
    removeSpaceMember(spaceId: string, userId: string): Promise<void>;

    // Pages belong to a space. No page-consuming UI exists yet, so this is
    // wired for later; the server returns page summaries (no body).
    listPages(spaceId: string): Promise<Page[]>;
}
