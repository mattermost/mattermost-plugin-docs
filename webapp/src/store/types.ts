// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space} from 'types/docs';

export type SpacesState = {
    byId: Record<string, Space>;
    order: string[];
};

export type PagesState = {
    byId: Record<string, Page>;
    bySpace: Record<string, string[]>;
};

export type DocsPluginState = {
    spaces: SpacesState;
    pages: PagesState;
};
