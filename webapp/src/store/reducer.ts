// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers} from 'redux';

import entities from './entities';

export {reindexAfterMove} from './entities';

const reducer = combineReducers({entities});

export default reducer;
