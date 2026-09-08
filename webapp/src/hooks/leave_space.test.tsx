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

const clientError = (status: number, serverErrorId?: string) =>
    new ClientError('', {message: 'nope', status_code: status, url: '/x', server_error_id: serverErrorId});

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
        mockLeaveSpace.mockRejectedValue(clientError(409, 'app.space.remove_member.last_member.app_error'));
        const {current} = render();

        await expect(current()).resolves.toBe(false);

        expect(mockGoHome).not.toHaveBeenCalled();
        expect(toast.error).toHaveBeenCalledWith(
            'Unable to leave Engineering',
            {description: expect.stringContaining('at least one member')},
        );
    });

    // The last-member wording tells the user to add a member, which does not lift a sole-admin
    // refusal — so this rule needs its own message, not the status it shares with the one above.
    it('names the administrator requirement on a last-admin refusal', async () => {
        mockLeaveSpace.mockRejectedValue(clientError(409, 'app.space.member.last_admin.app_error'));
        const {current} = render();

        await expect(current()).resolves.toBe(false);

        expect(toast.error).toHaveBeenCalledWith(
            'Unable to leave Engineering',
            {description: expect.stringContaining('at least one administrator')},
        );
    });

    it('reports a lock timeout as retryable rather than as a rule violation', async () => {
        mockLeaveSpace.mockRejectedValue(clientError(409, 'app.space.lock_timeout.app_error'));
        const {current} = render();

        await expect(current()).resolves.toBe(false);

        expect(toast.error).toHaveBeenCalledWith(
            'Unable to leave Engineering',
            {description: expect.stringContaining('Try again')},
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
