// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {ClientError} from '@mattermost/client';

import {makePage, makeSpace, makeTeam} from 'store/test_fixtures';

import {SpaceTypes} from './action_types';
import {addSpaceMember, addSpaceMembers, createSpace, fetchAllSpaces, isLastSpaceAdminError, isLastSpaceMemberError, isNotTeamMemberError, isSpaceLockTimeoutError, leaveSpace, movePage, removeSpaceMember} from './actions';

import {makeTestState} from '../../tests/react_testing_utils';

const mockAddSpaceMember = jest.fn();
const mockRemoveSpaceMember = jest.fn();
const mockMovePage = jest.fn();
const mockListPages = jest.fn();
const mockListSpaces = jest.fn();
const mockCreateSpace = jest.fn();

jest.mock('data', () => ({
    docsDataSource: {
        addSpaceMember: (...args: unknown[]) => mockAddSpaceMember(...args as []),
        removeSpaceMember: (...args: unknown[]) => mockRemoveSpaceMember(...args as []),
        movePage: (...args: unknown[]) => mockMovePage(...args as []),
        listPages: (...args: unknown[]) => mockListPages(...args as []),
        listSpaces: (...args: unknown[]) => mockListSpaces(...args as []),
        createSpace: (...args: unknown[]) => mockCreateSpace(...args as []),
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

describe('fetchAllSpaces', () => {
    beforeEach(() => jest.clearAllMocks());

    it('dispatches successful team results when another team fails', async () => {
        const team1 = makeTeam('team1', 'one');
        const team2 = makeTeam('team2', 'two');
        const space = makeSpace('space1', 'One', team1.id);
        const error = new Error('no access');
        mockListSpaces.mockImplementation((teamId: string) => (
            teamId === team1.id ? Promise.resolve([space]) : Promise.reject(error)
        ));
        const consoleError = jest.spyOn(console, 'error').mockImplementation(() => {});
        const state = makeTestState({teams: [team1, team2], currentUser: {id: 'user1'}});

        const {result, dispatch} = run((d, g) => fetchAllSpaces()(d as never, g as never, undefined as never), state);
        await result;

        expect(dispatch).toHaveBeenCalledWith({type: SpaceTypes.RECEIVED_SPACES, spaces: [space]});
        expect(consoleError).toHaveBeenCalledWith('Docs: failed to load spaces for team', team2.id, error);
        consoleError.mockRestore();
    });
});

describe('createSpace', () => {
    beforeEach(() => jest.clearAllMocks());

    it('rejects before calling the data source without a current team', async () => {
        const input = {title: 'New space', visibility: 'public' as const};

        const {result} = run((d, g) => createSpace(input)(d as never, g as never, undefined as never), makeTestState());

        await expect(result).rejects.toThrow('cannot create a space without a current team');
        expect(mockCreateSpace).not.toHaveBeenCalled();
    });
});

const LAST_MEMBER = 'app.space.remove_member.last_member.app_error';
const LAST_ADMIN = 'app.space.member.last_admin.app_error';
const LOCK = 'app.space.lock_timeout.app_error';

describe('409 discrimination on the removal routes', () => {
    const conflict = (id: string) =>
        new ClientError('', {message: 'nope', status_code: 409, url: '/x', server_error_id: id});

    // Three distinct rules answer 409 on these routes, so each predicate must key on the id. Keying
    // on the status made all three look like the last-member refusal, which sent a sole admin off to
    // add an ordinary member and reported a retryable lock timeout as a permanent rule violation.
    it('tells the three 409 rules apart', () => {
        expect(isLastSpaceMemberError(conflict(LAST_MEMBER))).toBe(true);
        expect(isLastSpaceAdminError(conflict(LAST_MEMBER))).toBe(false);
        expect(isSpaceLockTimeoutError(conflict(LAST_MEMBER))).toBe(false);

        expect(isLastSpaceAdminError(conflict(LAST_ADMIN))).toBe(true);
        expect(isLastSpaceMemberError(conflict(LAST_ADMIN))).toBe(false);

        expect(isSpaceLockTimeoutError(conflict(LOCK))).toBe(true);
        expect(isLastSpaceMemberError(conflict(LOCK))).toBe(false);
    });

    it('claims nothing for a 409 carrying no id, or a non-ClientError', () => {
        expect(isLastSpaceMemberError(new ClientError('', {message: 'nope', status_code: 409, url: '/x'}))).toBe(false);
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
