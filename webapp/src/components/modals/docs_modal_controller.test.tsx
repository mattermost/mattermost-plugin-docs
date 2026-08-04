// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, screen} from '@testing-library/react';
import React from 'react';

import DocsModalController, {useDocsModal, useDocsModalLayer} from './docs_modal_controller';
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

    describe('layering', () => {
        const Layer = ({name}: {name: string}) => {
            const {level, covered} = useDocsModalLayer();
            return <div>{`${name}: level ${level}, covered ${covered}`}</div>;
        };

        it('gives each stacked modal its own level, deepest first', () => {
            renderWithContext(<DocsModalController/>);

            act(() => {
                openDocsModal(<Layer name='Settings'/>);
            });
            act(() => {
                openDocsModal(<Layer name='Archive'/>);
            });

            expect(screen.getByText('Settings: level 0, covered 1')).toBeInTheDocument();
            expect(screen.getByText('Archive: level 1, covered 0')).toBeInTheDocument();
        });

        it('uncovers the modal below when the one above it closes', () => {
            renderWithContext(<DocsModalController/>);

            act(() => {
                openDocsModal(<Layer name='Settings'/>);
            });

            let archive = {id: '', close: () => {}};
            act(() => {
                archive = openDocsModal(<Layer name='Archive'/>);
            });

            expect(screen.getByText('Settings: level 0, covered 1')).toBeInTheDocument();

            act(() => {
                archive.close();
            });

            expect(screen.getByText('Settings: level 0, covered 0')).toBeInTheDocument();
        });

        it('reports a lone modal as uncovered, so a dialog outside the stack is unaffected', () => {
            renderWithContext(<Layer name='Standalone'/>);

            expect(screen.getByText('Standalone: level 0, covered 0')).toBeInTheDocument();
        });
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
