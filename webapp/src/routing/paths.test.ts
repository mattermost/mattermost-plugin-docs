// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    DOCS_DRAFTS_SEGMENT,
    DOCS_IMPORT_SEGMENT,
    EDIT_QUERY,
    RESERVED_SEGMENTS,
    SPACE_OR_PAGE_ID_PATTERN,
    draftPath,
    editDraftPath,
    editPagePath,
    importPath,
    overviewPath,
    pagePath,
    spaceImportPath,
    spacePath,
} from './paths';

describe('reserved URL segments', () => {
    // The one rule the scheme relies on, checked mechanically rather than by review.
    //
    // A segment that names something other than content is matched ahead of the id-bearing routes, so anything a
    // user could name identically becomes unreachable. Requiring these to be unmatchable as ids removes the
    // possibility instead of managing it — no reserved-word list to keep in step with the routes, and no
    // migration to run if custom slugs arrive later. A new segment that forgets the underscore fails here.
    it.each(RESERVED_SEGMENTS)('%s cannot be mistaken for a space or page id', (reserved) => {
        expect(SPACE_OR_PAGE_ID_PATTERN.test(reserved)).toBe(false);
    });

    it('has no duplicates, since two routes claiming one segment would be ambiguous', () => {
        expect(new Set(RESERVED_SEGMENTS).size).toBe(RESERVED_SEGMENTS.length);
    });

    // The pattern itself has to keep rejecting a leading underscore, or the rule above becomes vacuous while
    // still passing: every reserved segment would be a legal id, and nothing would say so.
    it('rejects a leading underscore in an id', () => {
        expect(SPACE_OR_PAGE_ID_PATTERN.test('_anything')).toBe(false);
        expect(SPACE_OR_PAGE_ID_PATTERN.test('has_underscores_inside')).toBe(true);
        expect(SPACE_OR_PAGE_ID_PATTERN.test('9fkw3f1oqjbgdfsyc6qsuoq5wc')).toBe(true);
    });
});

describe('path builders', () => {
    it('builds content paths from ids', () => {
        expect(spacePath('myteam', 'space1')).toBe('/myteam/spaces/space1');
        expect(pagePath('myteam', 'space1', 'pageX')).toBe('/myteam/spaces/space1/pageX');
    });

    it('builds the reserved paths from their segments', () => {
        expect(draftPath('myteam', 'space1', 'pageX')).toBe(`/myteam/spaces/space1/${DOCS_DRAFTS_SEGMENT}/pageX`);
        expect(importPath('myteam')).toBe(`/myteam/spaces/${DOCS_IMPORT_SEGMENT}`);
        expect(overviewPath('myteam', 'space1')).toBe('/myteam/spaces/space1/_overview');
        expect(spaceImportPath('myteam', 'space1')).toBe(`/myteam/spaces/space1/${DOCS_IMPORT_SEGMENT}`);
    });

    // Ids reach these builders from URLs and API responses, so they are escaped rather than trusted: an id that
    // ever arrived malformed must not be able to add segments of its own.
    it('escapes the values it interpolates', () => {
        expect(spacePath('my team', 'a/b')).toBe('/my%20team/spaces/a%2Fb');
        expect(draftPath('myteam', 'space1', '../elsewhere')).toBe(
            `/myteam/spaces/space1/${DOCS_DRAFTS_SEGMENT}/..%2Felsewhere`,
        );
    });
});

describe('editPagePath', () => {
    it('appends the edit query to the page path', () => {
        expect(editPagePath('team-1', 'eng', 'runbook')).toBe('/team-1/spaces/eng/runbook?edit=1');
    });

    it('builds on pagePath so escaping stays in one place', () => {
        expect(editPagePath('team 1', 'eng', 'run book')).toBe(`${pagePath('team 1', 'eng', 'run book')}?${EDIT_QUERY}=1`);
    });
});

describe('editDraftPath', () => {
    it('appends the edit query to the draft path', () => {
        expect(editDraftPath('team-1', 'eng', 'runbook')).toBe('/team-1/spaces/eng/_drafts/runbook?edit=1');
    });

    it('builds on draftPath so escaping stays in one place', () => {
        expect(editDraftPath('team 1', 'eng', 'run book')).toBe(`${draftPath('team 1', 'eng', 'run book')}?${EDIT_QUERY}=1`);
    });
});
