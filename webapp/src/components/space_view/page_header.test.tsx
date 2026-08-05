// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import {makeDraft, makePage, makeSpace} from 'store/test_fixtures';

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
            onPublish={jest.fn()}
            {...props}
        />,
    );

describe('PageHeader edit control', () => {
    it('offers Edit while reading', () => {
        renderHeader();

        expect(screen.getByRole('button', {name: 'Edit'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Close'})).not.toBeInTheDocument();
    });

    it('offers Close while editing', () => {
        renderHeader({editing: true});

        expect(screen.getByRole('button', {name: 'Close'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument();
    });

    // A label that changes on a button already holding focus is not reliably
    // re-announced, so the toggle has to carry its state.
    it('reports the unpressed mode while reading', () => {
        renderHeader();

        expect(screen.getByRole('button', {name: 'Edit'})).toHaveAttribute('aria-pressed', 'false');
    });

    it('reports the pressed mode while editing', () => {
        renderHeader({editing: true});

        expect(screen.getByRole('button', {name: 'Close'})).toHaveAttribute('aria-pressed', 'true');
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

describe('PageHeader publish controls', () => {
    const DRAFT = makeDraft('runbook', 'eng', 'Runbook');

    // With no published page the draft *is* the page, so committing it creates one.
    it('offers Publish while editing an unpublished page', () => {
        renderHeader({page: undefined, draft: DRAFT, editing: true});

        expect(screen.getByRole('button', {name: 'Publish'})).toBeEnabled();
        expect(screen.queryByRole('button', {name: 'Update'})).not.toBeInTheDocument();
    });

    // Alongside a published page the same draft means unpublished edits to it.
    it('offers Update while editing a page that has unpublished edits', () => {
        renderHeader({draft: DRAFT, editing: true});

        expect(screen.getByRole('button', {name: 'Update'})).toBeEnabled();
        expect(screen.queryByRole('button', {name: 'Publish'})).not.toBeInTheDocument();
    });

    // Nothing to apply, so the control says so rather than failing when pressed.
    it('disables Update when the page has no unpublished edits', () => {
        renderHeader({editing: true});

        expect(screen.getByRole('button', {name: 'Update'})).toBeDisabled();
    });

    // An unpublished page is invisible to everyone else until it lands, so the way
    // out of that state stays offered without a trip through edit mode.
    it('offers Publish while reading an unpublished page', () => {
        renderHeader({page: undefined, draft: DRAFT});

        expect(screen.getByRole('button', {name: 'Publish'})).toBeEnabled();
    });

    it('hides Update while reading a published page', () => {
        renderHeader({draft: DRAFT});

        expect(screen.queryByRole('button', {name: 'Update'})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Publish'})).not.toBeInTheDocument();
    });

    it('publishes on click', () => {
        const onPublish = jest.fn();
        renderHeader({page: undefined, draft: DRAFT, editing: true, onPublish});

        fireEvent.click(screen.getByRole('button', {name: 'Publish'}));

        expect(onPublish).toHaveBeenCalled();
    });
});
