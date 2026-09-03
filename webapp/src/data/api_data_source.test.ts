// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';

import {Client4} from 'mattermost-redux/client';

import type {Space} from 'types/docs';

import {apiDataSource} from './api_data_source';

const mockRestPost = jest.fn();

const jsonResponse = (body: unknown, ok = true, status = 200) => ({
    ok,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
    json: () => Promise.resolve(body),
} as Response);

jest.mock('client/rest', () => ({
    ...jest.requireActual('client/rest'),
    restPost: (...args: unknown[]) => mockRestPost(...args as []),
}));

describe('apiDataSource.createSpace', () => {
    beforeEach(() => {
        mockRestPost.mockReset();
        mockRestPost.mockResolvedValue({id: 'space1'} as Space);
    });

    const bodyOf = (): Record<string, unknown> => mockRestPost.mock.calls[0][1];

    // The server defaults an absent view_access to 'open', so a dropped field
    // silently publishes a space the user asked to keep private.
    it('sends view_access private when the form selects private', async () => {
        await apiDataSource.createSpace('team1', {title: 'Docs', view_access: 'private'});

        expect(bodyOf().view_access).toBe('private');
    });

    it('sends view_access open when the form selects open', async () => {
        await apiDataSource.createSpace('team1', {title: 'Docs', view_access: 'open'});

        expect(bodyOf().view_access).toBe('open');
    });

    it('always sends view_access, never omitting it', async () => {
        await apiDataSource.createSpace('team1', {title: 'Docs', view_access: 'private'});

        expect(bodyOf()).toHaveProperty('view_access');
    });

    it('omits default_permissions so the server applies the configured new-space template', async () => {
        await apiDataSource.createSpace('team1', {title: 'Docs', view_access: 'private'});

        expect(bodyOf()).not.toHaveProperty('default_permissions');
    });
});

describe('apiDataSource space access', () => {
    const fetchMock = jest.fn();
    const originalFetch = global.fetch;

    beforeEach(() => {
        fetchMock.mockReset();
        mockRestPost.mockReset();
        global.fetch = fetchMock as unknown as typeof fetch;
        Client4.setUrl('http://localhost:8065');
    });

    afterAll(() => {
        global.fetch = originalFetch;
    });

    it('addresses the paginated member route under the configured server url', async () => {
        fetchMock.mockResolvedValue(jsonResponse({items: [], page: 0, per_page: 100, has_more: false}));

        await apiDataSource.listSpaceMembers('space1');

        expect(fetchMock.mock.calls[0][0]).toBe('http://localhost:8065/plugins/com.mattermost.docs/api/v1/spaces/space1/members?page=0&per_page=100');
    });

    it('joins the caller through the same data-source seam', async () => {
        mockRestPost.mockResolvedValue({id: 'space1'} as Space);

        await apiDataSource.joinSpace('space1');

        expect(mockRestPost).toHaveBeenCalledWith(
            'http://localhost:8065/plugins/com.mattermost.docs/api/v1/spaces/space1/members/me',
            {},
        );
    });

    it('sends an explicit empty list when a member grant is cleared', async () => {
        fetchMock.mockResolvedValue(jsonResponse({user_id: 'user2', permissions: [], granted_permissions: []}));

        await apiDataSource.setSpaceMemberPermissions('space1', 'user2', []);

        const [, options] = fetchMock.mock.calls[0];
        expect(options.method).toBe('PUT');
        expect(JSON.parse(options.body)).toEqual({granted_permissions: []});
    });

    it('names the default-permissions field the server requires', async () => {
        fetchMock.mockResolvedValue(jsonResponse({id: 'space1', default_permissions: [], permissions: []}));

        await apiDataSource.setSpaceDefaultPermissions('space1', ['edit_page']);

        const [, options] = fetchMock.mock.calls[0];
        expect(JSON.parse(options.body)).toEqual({default_permissions: ['edit_page']});
    });

    it('sends a visibility change with the optimistic-lock baseline', async () => {
        fetchMock.mockResolvedValue(jsonResponse({id: 'space1', view_access: 'private', update_at: 101}));

        await apiDataSource.setSpaceViewAccess('space1', 'private', 100);

        const [url, options] = fetchMock.mock.calls[0];
        expect(url).toBe('http://localhost:8065/plugins/com.mattermost.docs/api/v1/spaces/space1');
        expect(options.method).toBe('PATCH');
        expect(JSON.parse(options.body)).toEqual({view_access: 'private', expected_update_at: 100});
    });

    it('raises the server details from a nested conflict envelope', async () => {
        fetchMock.mockResolvedValue(jsonResponse({
            error: {id: 'app.space.member.last_admin.app_error', message: 'Cannot demote the last admin.', status_code: 409},
            current_page: null,
        }, false, 409));

        await expect(apiDataSource.setSpaceMemberPermissions('space1', 'user2', [])).rejects.toMatchObject({
            status: 409,
            message: 'Cannot demote the last admin.',
            server_error_id: 'app.space.member.last_admin.app_error',
        });
    });

    it('raises the server details from a flat AppError body', async () => {
        fetchMock.mockResolvedValue(jsonResponse({id: 'app.space.member.user_not_found.app_error', message: 'Member not found.'}, false, 404));

        await expect(apiDataSource.setSpaceMemberPermissions('space1', 'user2', [])).rejects.toMatchObject({
            status: 404,
            message: 'Member not found.',
            server_error_id: 'app.space.member.user_not_found.app_error',
        });
    });

    it('still raises when the failure body is not JSON', async () => {
        fetchMock.mockResolvedValue({
            ok: false,
            status: 502,
            text: () => Promise.resolve('<html>bad gateway</html>'),
            json: () => Promise.reject(new Error('not json')),
        } as unknown as Response);

        await expect(apiDataSource.listSpaceMembers('space1')).rejects.toBeInstanceOf(RestError);
    });

    it('escapes ids so a path segment cannot be forged', async () => {
        fetchMock.mockResolvedValue(jsonResponse({user_id: 'x', permissions: [], granted_permissions: []}));

        await apiDataSource.setSpaceMemberPermissions('space/1', '../admin', []);

        expect(fetchMock.mock.calls[0][0]).toContain('/spaces/space%2F1/members/..%2Fadmin/permissions');
    });
});
