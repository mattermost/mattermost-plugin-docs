// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {isPaginationLimitError, listAll} from './rest';

const mockFetch = jest.fn();
const originalFetch = global.fetch;

jest.mock('mattermost-redux/client', () => ({
    Client4: {
        url: 'http://mattermost.test',
        getOptions: (options: unknown) => options,
    },
}));

const response = (body: unknown): Response => ({
    ok: true,
    text: async () => JSON.stringify(body),
}) as Response;

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
