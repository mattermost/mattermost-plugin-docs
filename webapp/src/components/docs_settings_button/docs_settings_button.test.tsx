// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import DocsSettingsButton from './docs_settings_button';

import {renderWithContext} from '../../../tests/react_testing_utils';

type WebappUtilsWindow = typeof window & {
    WebappUtils?: {openModalById?: jest.Mock};
};

describe('DocsSettingsButton', () => {
    afterEach(() => {
        delete (window as WebappUtilsWindow).WebappUtils;
    });

    it('opens the host User Settings modal by id', () => {
        const openModalById = jest.fn().mockReturnValue({type: 'OPEN_MODAL'});
        (window as WebappUtilsWindow).WebappUtils = {openModalById};

        renderWithContext(<DocsSettingsButton/>);
        fireEvent.click(screen.getByRole('button', {name: 'Settings'}));

        expect(openModalById).toHaveBeenCalledTimes(1);
        expect(openModalById.mock.calls[0][0]).toBe('user_settings');
    });

    it('no-ops without throwing when the host opener is absent', () => {
        renderWithContext(<DocsSettingsButton/>);
        expect(() => fireEvent.click(screen.getByRole('button', {name: 'Settings'}))).not.toThrow();
    });
});
