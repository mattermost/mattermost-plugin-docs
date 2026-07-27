// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import DocsSettingsButton from './docs_settings_button';

import {renderWithContext} from '../../../tests/react_testing_utils';

type WebappUtilsWindow = typeof window & {
    WebappUtils?: {modals?: {openModalById?: jest.Mock; canOpenModalId?: jest.Mock}};
};

function setHostModals(modals: {openModalById?: jest.Mock; canOpenModalId?: jest.Mock}) {
    (window as WebappUtilsWindow).WebappUtils = {modals};
}

describe('DocsSettingsButton', () => {
    afterEach(() => {
        delete (window as WebappUtilsWindow).WebappUtils;
    });

    it('dispatches the host User Settings modal when the id is publishable', () => {
        const openModalById = jest.fn().mockReturnValue({type: 'OPEN_MODAL'});
        const canOpenModalId = jest.fn().mockReturnValue(true);
        setHostModals({openModalById, canOpenModalId});

        renderWithContext(<DocsSettingsButton/>);
        fireEvent.click(screen.getByRole('button', {name: 'Settings'}));

        expect(canOpenModalId).toHaveBeenCalledWith('user_settings');
        expect(openModalById).toHaveBeenCalledTimes(1);
        expect(openModalById.mock.calls[0][0]).toBe('user_settings');
    });

    it('does not open when the host cannot publish the id', () => {
        const openModalById = jest.fn();
        const canOpenModalId = jest.fn().mockReturnValue(false);
        setHostModals({openModalById, canOpenModalId});

        renderWithContext(<DocsSettingsButton/>);
        fireEvent.click(screen.getByRole('button', {name: 'Settings'}));

        expect(openModalById).not.toHaveBeenCalled();
    });

    it('no-ops without throwing when the host modal API is absent', () => {
        renderWithContext(<DocsSettingsButton/>);
        expect(() => fireEvent.click(screen.getByRole('button', {name: 'Settings'}))).not.toThrow();
    });
});
