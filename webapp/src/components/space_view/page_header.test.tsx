// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {makePage, makeSpace} from 'store/test_fixtures';

import PageHeader from './page_header';

import {renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('webapp_globals', () => ({Timestamp: () => null}));
jest.mock('hooks/pages', () => ({useCreateRootPage: () => jest.fn()}));

// Stubbed at the hook boundary: mattermost-redux's preferences actions are
// published as ESM that jest doesn't transform.
jest.mock('hooks/favorites', () => ({
    useIsFavorite: () => false,
    useToggleFavorite: () => jest.fn(),
}));

const SPACE = makeSpace('eng', 'Engineering');
const PAGE = makePage('runbook', 'eng', 'Runbook');

const renderHeader = (props: Partial<React.ComponentProps<typeof PageHeader>> = {}) =>
    renderWithContext(
        <PageHeader
            space={SPACE}
            page={PAGE}
            treeOpen={false}
            editing={false}
            onTogglePages={jest.fn()}
            onToggleEdit={jest.fn()}
            {...props}
        />,
    );

describe('PageHeader edit control', () => {
    it('offers Edit while reading', () => {
        renderHeader();

        expect(screen.getByRole('button', {name: 'Edit'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Done'})).not.toBeInTheDocument();
    });

    it('offers Done while editing', () => {
        renderHeader({editing: true});

        expect(screen.getByRole('button', {name: 'Done'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument();
    });

    it('reports a click on the control', () => {
        const onToggleEdit = jest.fn();
        renderHeader({onToggleEdit});

        fireEvent.click(screen.getByRole('button', {name: 'Edit'}));

        expect(onToggleEdit).toHaveBeenCalledTimes(1);
    });

    it('offers no edit control on the space home, where no page is routed', () => {
        renderHeader({page: undefined});

        expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument();
    });
});
