// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The URL segments that name something other than content, and the id grammar they
// are kept apart from. Mirrored from webapp/src/routing/paths.ts rather than imported:
// these specs drive a built plugin over HTTP and share no module graph with it.
//
// Mirroring is the point here. A spec that imported the constants would pass against
// whatever the app currently calls these segments; holding a copy means a rename that
// misses this file is reported as the URL change it is.
export const DRAFTS_SEGMENT = '_drafts';
export const OVERVIEW_SEGMENT = '_overview';
export const IMPORT_SEGMENT = '_import';

export const RESERVED_SEGMENTS = [DRAFTS_SEGMENT, OVERVIEW_SEGMENT, IMPORT_SEGMENT] as const;

// SPACE_OR_PAGE_ID from routing/paths.ts, anchored: a lowercase alphanumeric, then
// lowercase alphanumerics, dashes, or underscores. A leading underscore is what it
// refuses, and that refusal is the whole reason the segments above are safe.
export const SPACE_OR_PAGE_ID_PATTERN = /^[a-z0-9][a-z0-9\-_]*$/;
