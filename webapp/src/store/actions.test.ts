// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {ClientError} from '@mattermost/client';

import {makePage} from 'store/test_fixtures';

import {addSpaceMember, addSpaceMembers, isLastSpaceMemberError, isNotTeamMemberError, leaveSpace, movePage, removeSpaceMember} from './actions';
import {SpaceTypes} from './action_types';

const mockAddSpaceMember = jest.fn();
const mockRemoveSpaceMember = jest.fn();
const mockMovePage = jest.fn();
const mockListPages = jest.fn();

jest.mock('data', () => ({
    docsDataSource: {
        addSpaceMember: (...args: unknown[]) => mockAddSpaceMember(...args as []),
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

const added = (userId: string) => ({type: SpaceTypes.ADDED_SPACE_MEMBER, spaceId: 'space1', userId});

describe('addSpaceMember', () => {
    beforeEach(() => jest.clearAllMocks());

    it('dispatches the add once the server accepts it', async () => {
        mockAddSpaceMember.mockResolvedValue({user_id: 'u1'});

        const {result, dispatch} = run((d, g) => addSpaceMember('space1', 'u1')(d as never, g as never, undefined as never));
        await result;

        expect(mockAddSpaceMember).toHaveBeenCalledWith('space1', 'u1');
        expect(dispatch).toHaveBeenCalledWith(added('u1'));
    });

    // It rejects rather than swallowing, because only the caller can tell a 403
    // ("not on this team") apart from a fault worth a generic message.
    it('rejects and dispatches nothing when the server refuses', async () => {
        mockAddSpaceMember.mockRejectedValue(new Error('nope'));

        const {result, dispatch} = run((d, g) => addSpaceMember('space1', 'u1')(d as never, g as never, undefined as never));

        await expect(result).rejects.toThrow('nope');
        expect(dispatch).not.toHaveBeenCalledWith(added('u1'));
    });
});

describe('removeSpaceMember', () => {
    beforeEach(() => jest.clearAllMocks());

    it('dispatches the removal once the server accepts it', async () => {
        mockRemoveSpaceMember.mockResolvedValue(undefined);

        const {result, dispatch} = run((d, g) => removeSpaceMember('space1', 'u1')(d as never, g as never, undefined as never));
        await result;

        expect(mockRemoveSpaceMember).toHaveBeenCalledWith('space1', 'u1');
        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId: 'space1', userId: 'u1'});
    });

    it('rejects when the server refuses, leaving the store alone', async () => {
        mockRemoveSpaceMember.mockRejectedValue(new Error('nope'));

        const {result, dispatch} = run((d, g) => removeSpaceMember('space1', 'u1')(d as never, g as never, undefined as never));

        await expect(result).rejects.toThrow('nope');
        expect(dispatch).not.toHaveBeenCalledWith({type: SpaceTypes.REMOVED_SPACE_MEMBER, spaceId: 'space1', userId: 'u1'});
    });
});

describe('addSpaceMembers', () => {
    beforeEach(() => jest.clearAllMocks());

    // A partly-failed batch has no single outcome, so the successes have already
    // landed by the time the wrapper resolves with the failures.
    it('resolves with only the failures and still dispatches the successes', async () => {
        const refusal = new Error('not on this team');
        mockAddSpaceMember.
            mockResolvedValueOnce({user_id: 'u1'}).
            mockRejectedValueOnce(refusal).
            mockResolvedValueOnce({user_id: 'u3'});

        const {result, dispatch} = run((d, g) => addSpaceMembers('space1', ['u1', 'u2', 'u3'])(d as never, g as never, undefined as never));
        const failed = await result;

        expect(failed).toEqual([{userId: 'u2', error: refusal}]);
        expect(dispatch).toHaveBeenCalledWith(added('u1'));
        expect(dispatch).toHaveBeenCalledWith(added('u3'));
        expect(dispatch).not.toHaveBeenCalledWith(added('u2'));
    });

    it('never rejects, even when every add fails', async () => {
        mockAddSpaceMember.mockRejectedValue(new Error('nope'));

        const {result} = run((d, g) => addSpaceMembers('space1', ['u1', 'u2'])(d as never, g as never, undefined as never));

        await expect(result).resolves.toHaveLength(2);
    });

    it('resolves empty for an empty batch without calling the server', async () => {
        const {result} = run((d, g) => addSpaceMembers('space1', [])(d as never, g as never, undefined as never));

        await expect(result).resolves.toEqual([]);
        expect(mockAddSpaceMember).not.toHaveBeenCalled();
    });
});

describe('isNotTeamMemberError', () => {
    it('recognises the server 403 and nothing else', () => {
        expect(isNotTeamMemberError(new ClientError('', {message: 'nope', status_code: 403, url: '/x'}))).toBe(true);
        expect(isNotTeamMemberError(new ClientError('', {message: 'nope', status_code: 409, url: '/x'}))).toBe(false);
        expect(isNotTeamMemberError(new Error('boom'))).toBe(false);
    });
});
