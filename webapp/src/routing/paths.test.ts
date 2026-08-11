// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {EDIT_QUERY, draftPath, editDraftPath, editPagePath, pagePath} from './paths';

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
