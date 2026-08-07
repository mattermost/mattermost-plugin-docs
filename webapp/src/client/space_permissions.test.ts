// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';

import {Client4} from 'mattermost-redux/client';

import {
    getSpaceMembers,
    setDefaultCapabilities,
    setMemberCapabilities,
} from './space_permissions';

const jsonResponse = (body: unknown, ok = true, status = 200) => ({
    ok,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
    json: () => Promise.resolve(body),
} as Response);

describe('client/space_permissions', () => {
    const fetchMock = jest.fn();

    beforeEach(() => {
        global.fetch = fetchMock as unknown as typeof fetch;
        Client4.setUrl('http://localhost:8065');
    });

    it('addresses the plugin routes under the configured server url', async () => {
        fetchMock.mockResolvedValue(jsonResponse({items: [], page: 0, per_page: 100, has_more: false}));

        await getSpaceMembers('space1', 0, 100);

        expect(fetchMock.mock.calls[0][0]).toBe('http://localhost:8065/plugins/com.mattermost.docs/api/v1/spaces/space1/members?page=0&per_page=100');
    });

    it('sends an explicit empty list when a grant is cleared', async () => {
        fetchMock.mockResolvedValue(jsonResponse({user_id: 'user2', capabilities: [], granted_capabilities: []}));

        await setMemberCapabilities('space1', 'user2', []);

        const [, options] = fetchMock.mock.calls[0];
        expect(options.method).toBe('PUT');
        expect(JSON.parse(options.body)).toEqual({granted_capabilities: []});
    });

    it('names the default-capabilities field the server requires', async () => {
        fetchMock.mockResolvedValue(jsonResponse({id: 'space1', default_capabilities: [], capabilities: []}));

        await setDefaultCapabilities('space1', ['edit_page']);

        const [, options] = fetchMock.mock.calls[0];
        expect(JSON.parse(options.body)).toEqual({default_capabilities: ['edit_page']});
    });

    it('raises the server message and status from the nested conflict envelope on a refusal', async () => {
        fetchMock.mockResolvedValue(jsonResponse({
            error: {id: 'app.space.member.last_admin.app_error', message: 'Cannot demote the last admin.', status_code: 409},
            current_page: null,
        }, false, 409));

        await expect(setMemberCapabilities('space1', 'user2', [])).rejects.toMatchObject({
            status: 409,
            message: 'Cannot demote the last admin.',
            server_error_id: 'app.space.member.last_admin.app_error',
        });
    });

    it('raises the server message and status from a flat AppError body on a non-conflict refusal', async () => {
        fetchMock.mockResolvedValue(jsonResponse({id: 'app.space.not_found.app_error', message: 'Space not found.'}, false, 404));

        await expect(setMemberCapabilities('space1', 'user2', [])).rejects.toMatchObject({
            status: 404,
            message: 'Space not found.',
            server_error_id: 'app.space.not_found.app_error',
        });
    });

    it('still raises when the failure body is not JSON', async () => {
        fetchMock.mockResolvedValue({
            ok: false,
            status: 502,
            text: () => Promise.resolve('<html>bad gateway</html>'),
            json: () => Promise.reject(new Error('not json')),
        } as unknown as Response);

        await expect(getSpaceMembers('space1', 0, 100)).rejects.toBeInstanceOf(RestError);
    });

    it('escapes ids so a path segment cannot be forged', async () => {
        fetchMock.mockResolvedValue(jsonResponse({user_id: 'x', capabilities: [], granted_capabilities: []}));

        await setMemberCapabilities('space/1', '../admin', []);

        expect(fetchMock.mock.calls[0][0]).toContain('/spaces/space%2F1/members/..%2Fadmin/capabilities');
    });
});
