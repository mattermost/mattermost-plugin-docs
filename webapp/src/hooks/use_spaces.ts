// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';

import type {Space} from 'types/docs';

// All spaces for the current context. The component never knows the source.
export function useSpaces(): Space[] {
    return docsDataSource.listSpaces();
}
