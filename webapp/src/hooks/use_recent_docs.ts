// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';
import {useMemo} from 'react';

import type {Page, Space} from 'types/docs';

// Recently-viewed spaces and pages for the switcher's default view.
export function useRecentDocs(): {spaces: Space[]; pages: Page[]} {
    return useMemo(() => ({
        spaces: docsDataSource.getRecentSpaces(),
        pages: docsDataSource.getRecentPages(),
    }), []);
}
