// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {ClientError} from '@mattermost/client';

import {makePage} from 'store/test_fixtures';

import {isLastSpaceMemberError, leaveSpace, movePage} from './actions';

const mockRemoveSpaceMember = jest.fn();
const mockMovePage = jest.fn();
const mockListPages = jest.fn();

jest.mock('data', () => ({
    docsDataSource: {
        removeSpaceMember: (...args: unknown[]) => mockRemoveSpaceMember(...args as []),
        movePage: (...args: unknown[]) => mockMovePage(...args as []),
        listPages: (...args: unknown[]) => mockListPages(...args as []),
    },
}));

jest.mock('mattermost-redux/selectors/entities/users', () => ({getCurrentUserId: () => 'user1'}));

const PAGE = makePage('page1', 'space1', 'Page');

// The reducer isn't under test here; the thunks only need getState + a spy. A
// dispatched thunk runs inline, so nested dispatches (e.g. movePage's reload) work.
const run = <T>(thunk: (dispatch: jest.Mock, getState: () => unknown) => T, state: unknown = {}) => {
    const getState = () => state;
    const dispatch: jest.Mock = jest.fn((action: unknown) =>
        (typeof action === 'function' ? (action as (d: jest.Mock, g: () => unknown) => unknown)(dispatch, getState) : action));

    return {result: thunk(dispatch, getState), dispatch};
};

const stateWithPage = {
    [`plugins-${manifest.id}`]: {entities: {pages: {[PAGE.id]: PAGE}}},
};

describe('isLastSpaceMemberError', () => {
    // The REST layer keeps only {message, status_code}, so 409 is all a caller has
    // to recognise "a space must keep one member" by.
    it('recognises the server 409 and nothing else', () => {
        expect(isLastSpaceMemberError(new ClientError('', {message: 'nope', status_code: 409, url: '/x'}))).toBe(true);
        expect(isLastSpaceMemberError(new ClientError('', {message: 'nope', status_code: 403, url: '/x'}))).toBe(false);
        expect(isLastSpaceMemberError(new Error('boom'))).toBe(false);
        expect(isLastSpaceMemberError(undefined)).toBe(false);
    });
});

describe('leaveSpace', () => {
    beforeEach(() => jest.clearAllMocks());

    it('removes the current user and drops the space from the store', async () => {
        mockRemoveSpaceMember.mockResolvedValue(undefined);

        const {result, dispatch} = run((d, g) => leaveSpace('space1')(d as never, g as never, undefined as never));
        await result;

        expect(mockRemoveSpaceMember).toHaveBeenCalledWith('space1', 'user1');
        expect(dispatch).toHaveBeenCalledWith(expect.objectContaining({spaceId: 'space1'}));
    });

    it('rejects so the caller can explain the failure', async () => {
        const error = new ClientError('', {message: 'last member', status_code: 409, url: '/x'});
        mockRemoveSpaceMember.mockRejectedValue(error);

        const {result} = run((d, g) => leaveSpace('space1')(d as never, g as never, undefined as never));

        await expect(result).rejects.toBe(error);
    });
});

describe('movePage', () => {
    beforeEach(() => jest.clearAllMocks());

    it('reindexes optimistically, then reconciles with the server page', async () => {
        mockMovePage.mockResolvedValue({...PAGE, parent_id: 'parent1'});

        const {result, dispatch} = run((d, g) => movePage('space1', PAGE.id, 'parent1', 0)(d as never, g as never, undefined as never), stateWithPage);
        await result;

        expect(dispatch.mock.calls[0][0]).toEqual(expect.objectContaining({pageId: PAGE.id, parentId: 'parent1', siblingIndex: 0}));
        expect(mockMovePage).toHaveBeenCalledWith('space1', PAGE.id, 'parent1', 0, PAGE.update_at);
    });

    // The optimistic reindex is wrong once the server refuses, so the list is
    // reloaded and the failure still reaches the caller to surface.
    it('restores server truth and rejects when the move fails', async () => {
        const error = new Error('boom');
        mockMovePage.mockRejectedValue(error);
        mockListPages.mockResolvedValue([PAGE]);

        const {result} = run((d, g) => movePage('space1', PAGE.id, 'parent1', 0)(d as never, g as never, undefined as never), stateWithPage);

        await expect(result).rejects.toBe(error);
        expect(mockListPages).toHaveBeenCalledWith('space1');
    });

    it('does nothing for a page that is not loaded', async () => {
        const {result} = run((d, g) => movePage('space1', 'ghost', '', 0)(d as never, g as never, undefined as never), stateWithPage);
        await result;

        expect(mockMovePage).not.toHaveBeenCalled();
    });
});
