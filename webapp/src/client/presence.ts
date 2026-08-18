// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {PageActiveEditors} from 'types/drafts';

import {doFetch} from './rest';

export function getPageActiveEditors(spaceId: string, pageId: string, signal?: AbortSignal): Promise<PageActiveEditors> {
    return doFetch<PageActiveEditors>(
        `/spaces/${encodeURIComponent(spaceId)}/pages/${encodeURIComponent(pageId)}/active-editors`,
        {signal},
    );
}
