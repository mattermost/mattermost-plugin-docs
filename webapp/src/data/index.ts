// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {DocsDataSource} from './docs_data_source';
import {mockDataSource} from './mock_data_source';

// The single active data source. Swapped for the API-backed source once the
// Docs server contract exists; hooks depend only on this, never on the source
// implementation, so the swap touches no UI.
export const docsDataSource: DocsDataSource = mockDataSource;

export type {DocsDataSource} from './docs_data_source';
