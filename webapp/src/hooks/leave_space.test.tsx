// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {renderHook} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';

import {ClientError} from '@mattermost/client';

import {makeSpace} from 'store/test_fixtures';

import {toast} from 'components/toast';

import {useLeaveSpace} from './leave_space';

import {makeTestStore} from '../../tests/react_testing_utils';

const mockLeaveSpace = jest.fn();
const mockGoHome = jest.fn();
let mockRoutedSpaceId: string | undefined;

jest.mock('store/actions', () => ({
    ...jest.requireActual('store/actions'),
    leaveSpace: (spaceId: string) => async () => mockLeaveSpace(spaceId),
}));

jest.mock('hooks/navigation', () => ({
    useDocsNavigation: () => ({spaceId: mockRoutedSpaceId, goHome: mockGoHome}),
}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

const SPACE = makeSpace('eng', 'Engineering');

const render = () => {
    const store = makeTestStore();
    const wrapper = ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>
            <IntlProvider
                locale='en'
                messages={{}}
            >
                {children}
            </IntlProvider>
        </Provider>
    );

    return renderHook(() => useLeaveSpace(SPACE), {wrapper}).result;
};

const clientError = (status: number) => new ClientError('', {message: 'nope', status_code: status, url: '/x'});

describe('useLeaveSpace', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockRoutedSpaceId = undefined;
        mockLeaveSpace.mockResolvedValue(undefined);
    });

    it('leaves the space and stays put when viewing something else', async () => {
        const {current} = render();

        await expect(current()).resolves.toBe(true);

        expect(mockLeaveSpace).toHaveBeenCalledWith(SPACE.id);
        expect(mockGoHome).not.toHaveBeenCalled();
        expect(toast.error).not.toHaveBeenCalled();
    });

    it('navigates home when the space just left is the one being viewed', async () => {
        mockRoutedSpaceId = SPACE.id;
        const {current} = render();

        await expect(current()).resolves.toBe(true);

        expect(mockGoHome).toHaveBeenCalled();
    });

    // The server's 409 means "a space must keep one member", which is actionable;
    // reporting it as a generic failure would tell the user nothing.
    it('explains a last-member refusal specifically', async () => {
        mockLeaveSpace.mockRejectedValue(clientError(409));
        const {current} = render();

        await expect(current()).resolves.toBe(false);

        expect(mockGoHome).not.toHaveBeenCalled();
        expect(toast.error).toHaveBeenCalledWith(
            'Unable to leave Engineering',
            {description: expect.stringContaining('at least one member')},
        );
    });

    it('falls back to a generic message for any other failure', async () => {
        mockLeaveSpace.mockRejectedValue(clientError(500));
        const {current} = render();

        await expect(current()).resolves.toBe(false);

        expect(toast.error).toHaveBeenCalledWith(
            'Unable to leave Engineering',
            {description: 'Something went wrong. Please try again.'},
        );
    });
});
