// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {makePage, makeSpace, makeTeam} from 'store/test_fixtures';

import DocsSwitcher from './docs_switcher';

import {renderWithContext} from '../../../tests/react_testing_utils';

const state = {
    docs: {
        spaces: {byId: {eng: makeSpace('eng', 'Engineering'), sales: makeSpace('sales', 'Sales')}, order: ['eng', 'sales']},
        pages: {byId: {}, bySpace: {}},
    },
    currentTeam: makeTeam('team1', 'myteam'),
};

const stateWithPage = {
    docs: {
        spaces: {byId: {eng: makeSpace('eng', 'Engineering')}, order: ['eng']},
        pages: {byId: {onboarding: makePage('onboarding', 'eng', 'Onboarding')}, bySpace: {eng: ['onboarding']}},
    },
    currentTeam: makeTeam('team1', 'myteam'),
};

function renderSwitcher(onClose = jest.fn()) {
    return renderWithContext(<DocsSwitcher onClose={onClose}/>, {state});
}

describe('DocsSwitcher', () => {
    it('renders the search combobox and lists the user spaces', () => {
        renderSwitcher();

        expect(screen.getByRole('combobox')).toBeInTheDocument();
        expect(screen.getByRole('option', {name: /Engineering/})).toBeInTheDocument();
        expect(screen.getByRole('option', {name: /Sales/})).toBeInTheDocument();
    });

    it('filters results by query', () => {
        renderSwitcher();

        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'sales'}});

        expect(screen.getByRole('option', {name: /Sales/})).toBeInTheDocument();
        expect(screen.queryByRole('option', {name: /Engineering/})).not.toBeInTheDocument();
    });

    it('shows an empty state when nothing matches', () => {
        renderSwitcher();

        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'zzz'}});
        expect(screen.getByText('No spaces or pages found')).toBeInTheDocument();
    });

    it('navigates to a space on click and closes', () => {
        const onClose = jest.fn();
        const {history} = renderSwitcher(onClose);

        fireEvent.click(screen.getByRole('option', {name: /Engineering/}));

        expect(history.location.pathname).toBe('/myteam/spaces/eng');
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('selects the highlighted result with Enter after keyboard navigation', () => {
        const {history} = renderSwitcher();

        const combobox = screen.getByRole('combobox');
        fireEvent.keyDown(combobox, {key: 'ArrowDown'});
        fireEvent.keyDown(combobox, {key: 'Enter'});

        expect(history.location.pathname).toBe('/myteam/spaces/sales');
    });

    it('navigates to a page result within its space', () => {
        const {history} = renderWithContext(<DocsSwitcher onClose={jest.fn()}/>, {state: stateWithPage});

        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'onboard'}});
        fireEvent.click(screen.getByRole('option', {name: /Onboarding/}));

        expect(history.location.pathname).toBe('/myteam/spaces/eng/onboarding');
    });
});
