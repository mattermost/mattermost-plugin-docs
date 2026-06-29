// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';

import type {Space} from 'types/docs';

// A single space by opaque id, or undefined when none is selected/found.
export function useSpace(id?: string): Space | undefined {
    return id ? docsDataSource.getSpace(id) : undefined;
}
