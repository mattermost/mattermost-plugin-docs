// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {type UseDispatch, useDispatch, useSelector, useStore} from 'react-redux';
import type {Store} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import type {DocsDispatch} from 'types/store';

// The `as DocsDispatch` cast is currently required because mattermost-redux
// messes with the type definition for useDispatch.
export const useAppDispatch = (useDispatch as UseDispatch).withTypes<DocsDispatch>();
export const useAppSelector = useSelector.withTypes<GlobalState>();
export const useAppStore = useStore.withTypes<Store<GlobalState>>();
