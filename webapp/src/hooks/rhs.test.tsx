// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {useRhs} from './rhs';

import {renderWithContext} from '../../tests/react_testing_utils';

const PAGE_URL = '/myteam/spaces/eng/runbook';

// Exercised through a component, since the hook reads and writes the router.
const Probe = () => {
    const {rhs, openRhs, closeRhs, toggleRhs} = useRhs();

    return (
        <>
            <span data-testid='state'>{rhs ? `${rhs.id}:${rhs.view ?? 'root'}` : 'closed'}</span>
            <button
                type='button'
                onClick={() => toggleRhs('info')}
            >{'toggle info'}</button>
            <button
                type='button'
                onClick={() => toggleRhs('comments')}
            >{'toggle comments'}</button>
            <button
                type='button'
                onClick={() => openRhs('info', 'members')}
            >{'open members'}</button>
            <button
                type='button'
                onClick={closeRhs}
            >{'close'}</button>
        </>
    );
};

const renderProbe = (route = PAGE_URL) => renderWithContext(<Probe/>, {route});

const state = () => screen.getByTestId('state').textContent;

const press = (name: string) => fireEvent.click(screen.getByRole('button', {name}));

describe('useRhs', () => {
    it('reads the open panel out of the URL', () => {
        renderProbe(`${PAGE_URL}?rhs=info&rhsView=members`);

        expect(state()).toBe('info:members');
    });

    it('reads an unknown panel as closed, so a stale URL degrades to the page', () => {
        renderProbe(`${PAGE_URL}?rhs=nonsense`);

        expect(state()).toBe('closed');
    });

    it('opens and closes on the same control', () => {
        renderProbe();

        press('toggle info');
        expect(state()).toBe('info:root');

        press('toggle info');
        expect(state()).toBe('closed');
    });

    it('swaps panels rather than stacking them', () => {
        renderProbe();

        press('toggle info');
        press('toggle comments');

        expect(state()).toBe('comments:root');
    });

    it('drops the sub-view when the panel closes', () => {
        const {history} = renderProbe();

        press('open members');
        press('close');

        expect(history.location.search).toBe('');
    });

    // Back should leave the page, not walk back through every panel the reader
    // opened on the way — so each change replaces the entry instead of pushing one.
    it('replaces the history entry instead of pushing one', () => {
        const {history} = renderProbe();
        const before = history.length;

        press('toggle info');
        press('open members');
        press('close');

        expect(history.length).toBe(before);
        expect(history.location.pathname).toBe(PAGE_URL);
    });

    // Edit mode is a query too, and toggling a panel must not drop the editor.
    it('leaves other queries alone', () => {
        const {history} = renderProbe(`${PAGE_URL}?edit=1`);

        press('toggle info');
        expect(history.location.search).toBe('?edit=1&rhs=info');

        press('toggle info');
        expect(history.location.search).toBe('?edit=1');
    });
});
