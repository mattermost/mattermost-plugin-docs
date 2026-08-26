// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';

import {Client4} from 'mattermost-redux/client';

import {
    listAllSpaceMembers,
    setDefaultPermissions,
    setMemberPermissions,
    setSpaceViewAccess,
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

        await listAllSpaceMembers('space1');

        expect(fetchMock.mock.calls[0][0]).toBe('http://localhost:8065/plugins/com.mattermost.docs/api/v1/spaces/space1/members?page=0&per_page=100');
    });

    it('sends an explicit empty list when a grant is cleared', async () => {
        fetchMock.mockResolvedValue(jsonResponse({user_id: 'user2', permissions: [], granted_permissions: []}));

        await setMemberPermissions('space1', 'user2', []);

        const [, options] = fetchMock.mock.calls[0];
        expect(options.method).toBe('PUT');
        expect(JSON.parse(options.body)).toEqual({granted_permissions: []});
    });

    it('names the default-permissions field the server requires', async () => {
        fetchMock.mockResolvedValue(jsonResponse({id: 'space1', default_permissions: [], permissions: []}));

        await setDefaultPermissions('space1', ['edit_page']);

        const [, options] = fetchMock.mock.calls[0];
        expect(JSON.parse(options.body)).toEqual({default_permissions: ['edit_page']});
    });

    it('sends a visibility change as a PATCH carrying the optimistic-lock baseline', async () => {
        fetchMock.mockResolvedValue(jsonResponse({id: 'space1', default_permissions: [], permissions: [], view_access: 'private', update_at: 101}));

        await setSpaceViewAccess('space1', 'private', 100);

        const [url, options] = fetchMock.mock.calls[0];
        expect(url).toBe('http://localhost:8065/plugins/com.mattermost.docs/api/v1/spaces/space1');
        expect(options.method).toBe('PATCH');
        expect(JSON.parse(options.body)).toEqual({view_access: 'private', expected_update_at: 100});
    });

    it('raises the server message and status from the nested conflict envelope on a refusal', async () => {
        fetchMock.mockResolvedValue(jsonResponse({
            error: {id: 'app.space.member.last_admin.app_error', message: 'Cannot demote the last admin.', status_code: 409},
            current_page: null,
        }, false, 409));

        await expect(setMemberPermissions('space1', 'user2', [])).rejects.toMatchObject({
            status: 409,
            message: 'Cannot demote the last admin.',
            server_error_id: 'app.space.member.last_admin.app_error',
        });
    });

    // A real id and status this route can answer with. A space the caller cannot see answers 403,
    // never 404: the server deliberately reports "denied" and "does not exist" identically so a
    // caller cannot probe which it is. Modelling a 404 for that case would document behaviour the
    // server was built not to have.
    it('raises the server message and status from a flat AppError body on a non-conflict refusal', async () => {
        fetchMock.mockResolvedValue(jsonResponse({id: 'app.space.member.user_not_found.app_error', message: 'Member not found.'}, false, 404));

        await expect(setMemberPermissions('space1', 'user2', [])).rejects.toMatchObject({
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

        await expect(listAllSpaceMembers('space1')).rejects.toBeInstanceOf(RestError);
    });

    it('escapes ids so a path segment cannot be forged', async () => {
        fetchMock.mockResolvedValue(jsonResponse({user_id: 'x', permissions: [], granted_permissions: []}));

        await setMemberPermissions('space/1', '../admin', []);

        expect(fetchMock.mock.calls[0][0]).toContain('/spaces/space%2F1/members/..%2Fadmin/permissions');
    });
});
