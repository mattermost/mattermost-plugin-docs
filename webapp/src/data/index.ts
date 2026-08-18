// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {apiDataSource} from './api_data_source';
import type {DocsDataSource} from './docs_data_source';

// The single active data source: the Docs plugin REST API. Thunks depend only
// on this, never on the transport, so a fake can be substituted in tests.
export const docsDataSource: DocsDataSource = apiDataSource;

export type {DocsDataSource} from './docs_data_source';
