// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {useFullscreen} from './fullscreen';

import {renderWithContext} from '../../tests/react_testing_utils';

const PAGE_URL = '/myteam/spaces/eng/runbook';

const Probe = () => {
    const {fullscreen, toggleFullscreen} = useFullscreen();

    return (
        <>
            <span data-testid='state'>{fullscreen ? 'fullscreen' : 'windowed'}</span>
            <button
                type='button'
                onClick={toggleFullscreen}
            >{'toggle'}</button>
        </>
    );
};

const renderProbe = (route = PAGE_URL) => renderWithContext(<Probe/>, {route});

const state = () => screen.getByTestId('state').textContent;

const press = (name: string) => fireEvent.click(screen.getByRole('button', {name}));

describe('useFullscreen', () => {
    it('reads the mode out of the URL', () => {
        renderProbe(`${PAGE_URL}?fs=1`);

        expect(state()).toBe('fullscreen');
    });

    it('starts windowed', () => {
        renderProbe();

        expect(state()).toBe('windowed');
    });

    it('toggles both ways', () => {
        renderProbe();

        press('toggle');
        expect(state()).toBe('fullscreen');

        press('toggle');
        expect(state()).toBe('windowed');
    });

    // Back should leave the page, not undo a resize of the view.
    it('replaces the history entry instead of pushing one', () => {
        const {history} = renderProbe();
        const before = history.length;

        press('toggle');

        expect(history.length).toBe(before);
        expect(history.location.search).toBe('?fs=1');
    });

    it('leaves other queries alone', () => {
        const {history} = renderProbe(`${PAGE_URL}?rhs=comments`);

        press('toggle');

        expect(history.location.search).toBe('?rhs=comments&fs=1');
    });
});
