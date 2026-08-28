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

// SPACE_OR_PAGE_ID from routing/paths.ts: a lowercase alphanumeric, then lowercase
// alphanumerics, dashes, or underscores. Kept as source text and anchored separately,
// as paths.ts keeps it, so a URL assertion and the membership test below are built from
// one grammar rather than two that can drift. Server ids are alphanumeric today, but a
// spec that assumed only that would fail the day custom slugs arrive — which the scheme
// is designed to allow.
export const SPACE_OR_PAGE_ID = '[a-z0-9][a-z0-9\\-_]*';

// A leading underscore is what the grammar refuses, and that refusal is the whole reason
// the segments above are safe.
export const SPACE_OR_PAGE_ID_PATTERN = new RegExp(`^${SPACE_OR_PAGE_ID}$`);
