// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDispatch} from 'react-redux';

import type {DocsDispatch} from 'types/store';

export function useDocsDispatch(): DocsDispatch {
    return useDispatch<DocsDispatch>();
}
