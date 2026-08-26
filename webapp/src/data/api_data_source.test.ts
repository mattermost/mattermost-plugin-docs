// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Space} from 'types/docs';

import {apiDataSource} from './api_data_source';

const mockRestPost = jest.fn();

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
});
