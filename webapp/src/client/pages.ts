// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from 'types/docs';

import {doFetch} from './rest';

export function getPage(spaceId: string, pageId: string, signal?: AbortSignal): Promise<Page> {
    return doFetch<Page>(
        `/spaces/${encodeURIComponent(spaceId)}/pages/${encodeURIComponent(pageId)}`,
        {signal},
    );
}
