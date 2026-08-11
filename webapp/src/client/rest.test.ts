// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Client4} from 'mattermost-redux/client';

import {apiUrl, isPaginationLimitError, listAll, siteRoot} from './rest';

const mockFetch = jest.fn();
const originalFetch = global.fetch;

jest.mock('mattermost-redux/client', () => ({
    Client4: {
        getUrl: jest.fn(() => 'http://mattermost.test'),
        getOptions: (options: unknown) => options,
    },
}));

const mockGetUrl = Client4.getUrl as jest.MockedFunction<typeof Client4.getUrl>;

const response = (body: unknown): Response => ({
    ok: true,
    text: async () => JSON.stringify(body),
}) as Response;

describe('siteRoot', () => {
    const mattermostWindow = window as unknown as {basename?: string};

    afterEach(() => {
        mockGetUrl.mockReturnValue('http://mattermost.test');
        delete mattermostWindow.basename;
    });

    it('uses the configured client URL and removes its trailing slash', () => {
        mockGetUrl.mockReturnValue('https://mattermost.test/mattermost/');

        expect(siteRoot()).toBe('https://mattermost.test/mattermost');
        expect(apiUrl()).toBe('https://mattermost.test/mattermost/plugins/com.mattermost.docs/api/v1');
    });

    it('resolves a relative configured URL to an absolute URL', () => {
        mockGetUrl.mockReturnValue('/mattermost');

        expect(siteRoot()).toBe('http://localhost:8065/mattermost');
    });

    it('falls back to the host basename when the client URL is not configured', () => {
        mockGetUrl.mockReturnValue('');
        mattermostWindow.basename = '/mattermost';

        expect(siteRoot()).toBe('http://localhost:8065/mattermost');
    });
});

describe('listAll', () => {
    beforeEach(() => {
        mockFetch.mockReset();
        global.fetch = mockFetch as typeof fetch;
    });

    afterAll(() => {
        global.fetch = originalFetch;
    });

    it('returns every page once the server reports completion', async () => {
        mockFetch.
            mockResolvedValueOnce(response({items: ['one'], page: 0, per_page: 100, has_more: true})).
            mockResolvedValueOnce(response({items: ['two'], page: 1, per_page: 100, has_more: false}));

        await expect(listAll((query) => `/items?${query}`)).resolves.toEqual(['one', 'two']);
        expect(mockFetch).toHaveBeenCalledTimes(2);
        expect(mockFetch.mock.calls.map(([url]) => url)).toEqual([
            '/items?page=0&per_page=100',
            '/items?page=1&per_page=100',
        ]);
    });

    it('rejects with the partial list at the safety limit', async () => {
        mockFetch.mockResolvedValue(response({items: ['item'], page: 0, per_page: 100, has_more: true}));

        const error = await listAll<string>((query) => `/items?${query}`).then(
            () => undefined,
            (reason: unknown) => reason,
        );

        expect(isPaginationLimitError(error)).toBe(true);
        expect(isPaginationLimitError(error) && error.cause.items).toHaveLength(1000);
        expect(mockFetch).toHaveBeenCalledTimes(1000);
    });
});
