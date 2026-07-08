// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';

import type {CreateSpaceInput, Space} from 'types/docs';
import type {DocsThunkAction} from 'types/store';

import {PageTypes, SpaceTypes} from './action_types';

export function fetchSpaces(): DocsThunkAction<void> {
    return (dispatch) => {
        dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces: docsDataSource.listSpaces()});
    };
}

// Bootstraps pages for one space, or for every known space when called with no
// argument — there's no bulk "list all pages" on the data source yet.
export function fetchPages(spaceId?: string): DocsThunkAction<void> {
    return (dispatch) => {
        const spaceIds = spaceId ? [spaceId] : docsDataSource.listSpaces().map((space) => space.id);
        const pages = spaceIds.flatMap((id) => docsDataSource.listPages(id));
        dispatch({type: PageTypes.RECEIVED_PAGES, pages});
    };
}

export function createSpace(input: CreateSpaceInput): DocsThunkAction<Space> {
    return (dispatch) => {
        const space = docsDataSource.createSpace(input);
        dispatch({type: SpaceTypes.CREATED_SPACE, space});
        return space;
    };
}

export function leaveSpace(spaceId: string): DocsThunkAction<void> {
    return (dispatch) => {
        dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
    };
}
