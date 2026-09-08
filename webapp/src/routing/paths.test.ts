// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {EDIT_QUERY, draftPath, editDraftPath, editPagePath, pagePath, routedSpaceId} from './paths';

describe('routedSpaceId', () => {
    it.each([
        ['/team-1/spaces/eng', 'eng'],
        ['/team-1/spaces/eng/runbook', 'eng'],
        ['/team-1/spaces/eng/overview', 'eng'],
        ['/team-1/spaces/eng/drafts/runbook', 'eng'],
    ])('reads the space from %s', (pathname, expected) => {
        expect(routedSpaceId(pathname)).toBe(expected);
    });

    it.each([
        '/team-1/spaces',
        '/team-1/channels/town-square',
        '/team-1/spaces/Invalid',
    ])('returns undefined for %s', (pathname) => {
        expect(routedSpaceId(pathname)).toBeUndefined();
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
        expect(editDraftPath('team-1', 'eng', 'runbook')).toBe('/team-1/spaces/eng/drafts/runbook?edit=1');
    });

    it('builds on draftPath so escaping stays in one place', () => {
        expect(editDraftPath('team 1', 'eng', 'run book')).toBe(`${draftPath('team 1', 'eng', 'run book')}?${EDIT_QUERY}=1`);
    });
});
