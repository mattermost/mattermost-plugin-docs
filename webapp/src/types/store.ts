// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Action, Store as BaseStore} from 'redux';
import type {ThunkAction, ThunkDispatch} from 'redux-thunk';

import type {GlobalState} from '@mattermost/types/store';

export type DocsDispatch = ThunkDispatch<GlobalState, unknown, Action>;

export type DocsThunkAction<R = void> = ThunkAction<R, GlobalState, unknown, Action>;

// The host (core) store, as handed to Plugin.initialize's second argument.
export type DocsStore = BaseStore<GlobalState> & {dispatch: DocsDispatch};
