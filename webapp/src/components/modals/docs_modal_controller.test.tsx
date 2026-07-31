// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, screen} from '@testing-library/react';
import React from 'react';

import DocsModalController, {useDocsModal} from './docs_modal_controller';
import {closeAllDocsModals, openDocsModal} from './modal_store';

import {renderWithContext} from '../../../tests/react_testing_utils';

describe('DocsModalController', () => {
    afterEach(() => {
        act(() => {
            closeAllDocsModals();
        });
    });

    it('renders a modal opened imperatively and closes it via the handle', () => {
        renderWithContext(<DocsModalController/>);

        let handle = {id: '', close: () => {}};
        act(() => {
            handle = openDocsModal(<div>{'First modal'}</div>);
        });

        expect(screen.getByText('First modal')).toBeInTheDocument();

        act(() => {
            handle.close();
        });

        expect(screen.queryByText('First modal')).not.toBeInTheDocument();
    });

    it('stacks nested modals and pops only the closed one', () => {
        renderWithContext(<DocsModalController/>);

        act(() => {
            openDocsModal(<div>{'Outer'}</div>);
        });

        let inner = {id: '', close: () => {}};
        act(() => {
            inner = openDocsModal(<div>{'Inner'}</div>);
        });

        expect(screen.getByText('Outer')).toBeInTheDocument();
        expect(screen.getByText('Inner')).toBeInTheDocument();

        act(() => {
            inner.close();
        });

        expect(screen.getByText('Outer')).toBeInTheDocument();
        expect(screen.queryByText('Inner')).not.toBeInTheDocument();
    });

    it('passes the handle to a render function and exposes it through context', () => {
        const Content = () => {
            const handle = useDocsModal();

            return (
                <button
                    type='button'
                    onClick={handle?.close}
                >
                    {'Close from context'}
                </button>
            );
        };

        renderWithContext(<DocsModalController/>);

        act(() => {
            openDocsModal(() => <Content/>);
        });

        act(() => {
            screen.getByRole('button', {name: 'Close from context'}).click();
        });

        expect(screen.queryByRole('button', {name: 'Close from context'})).not.toBeInTheDocument();
    });
});
