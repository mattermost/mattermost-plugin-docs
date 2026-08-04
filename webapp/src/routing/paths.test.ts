// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {EDIT_QUERY, editPagePath, pagePath} from './paths';

describe('editPagePath', () => {
    it('appends the edit query to the page path', () => {
        expect(editPagePath('team-1', 'eng', 'runbook')).toBe('/team-1/spaces/eng/runbook?edit=1');
    });

    it('builds on pagePath so escaping stays in one place', () => {
        expect(editPagePath('team 1', 'eng', 'run book')).toBe(`${pagePath('team 1', 'eng', 'run book')}?${EDIT_QUERY}=1`);
    });
});
