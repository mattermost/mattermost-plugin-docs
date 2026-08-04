// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makePage} from 'store/test_fixtures';

import PageContent from './page_content';

import {renderWithContext} from '../../../../tests/react_testing_utils';

const mockUpdatePage = jest.fn();
const mockToastError = jest.fn();

// updatePage is a thunk, so the mock has to be a thunk too rather than a bare
// promise — otherwise the thunk middleware never touches it and the rejection
// in the failed-write test goes unhandled.
jest.mock('store/actions', () => ({
    updatePage: (...args: unknown[]) => () => mockUpdatePage(...args),
}));
jest.mock('components/toast', () => ({toast: {error: (...args: unknown[]) => mockToastError(...args)}}));
jest.mock('hooks/members', () => ({useUserProfile: () => undefined}));

const PAGE = makePage('runbook', 'eng', 'Runbook');

const content = (props: Partial<React.ComponentProps<typeof PageContent>> = {}) => (
    <PageContent
        page={PAGE}
        editing={true}
        {...props}
    />
);

const renderContent = () => renderWithContext(content());

const field = () => screen.getByRole('textbox', {name: 'Page title'});

describe('PageContent title commit', () => {
    beforeEach(() => {
        mockUpdatePage.mockReset().mockReturnValue(Promise.resolve(PAGE));
        mockToastError.mockReset();
    });

    it('does not write while typing', () => {
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});

        expect(mockUpdatePage).not.toHaveBeenCalled();
    });

    it('writes the trimmed title on Enter', async () => {
        renderContent();

        fireEvent.change(field(), {target: {value: '  Runbooks  '}});
        fireEvent.keyDown(field(), {key: 'Enter'});

        await waitFor(() => expect(mockUpdatePage).toHaveBeenCalledWith('eng', 'runbook', {title: 'Runbooks'}));
    });

    it('writes on blur', async () => {
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.blur(field());

        await waitFor(() => expect(mockUpdatePage).toHaveBeenCalledWith('eng', 'runbook', {title: 'Runbooks'}));
    });

    it('does not write when the title is unchanged', () => {
        renderContent();

        fireEvent.keyDown(field(), {key: 'Enter'});

        expect(mockUpdatePage).not.toHaveBeenCalled();
    });

    it('writes an emptied title, which the server accepts', async () => {
        renderContent();

        fireEvent.change(field(), {target: {value: ''}});
        fireEvent.keyDown(field(), {key: 'Enter'});

        await waitFor(() => expect(mockUpdatePage).toHaveBeenCalledWith('eng', 'runbook', {title: ''}));
    });

    it('reverts on Escape without writing', () => {
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Escape'});

        expect(field()).toHaveValue('Runbook');
        expect(mockUpdatePage).not.toHaveBeenCalled();
    });

    it('keeps the typed title and reports a failed write', async () => {
        mockUpdatePage.mockReturnValue(Promise.reject(new Error('nope')));
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Enter'});

        await waitFor(() => expect(mockToastError).toHaveBeenCalled());
        expect(field()).toHaveValue('Runbooks');
    });

    it('does not fire a second write while the first is still in flight', async () => {
        let resolveWrite: (value: typeof PAGE) => void = () => {};
        mockUpdatePage.mockReturnValue(new Promise((resolve) => {
            resolveWrite = resolve;
        }));
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Enter'});
        fireEvent.blur(field());

        expect(mockUpdatePage).toHaveBeenCalledTimes(1);

        resolveWrite(PAGE);
        await waitFor(() => expect(mockUpdatePage).toHaveBeenCalledTimes(1));
    });

    it('sends what was typed while the first write was in flight, once it settles', async () => {
        let resolveWrite: (value: typeof PAGE) => void = () => {};
        mockUpdatePage.mockReturnValueOnce(new Promise((resolve) => {
            resolveWrite = resolve;
        }));
        const {rerender} = renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Enter'});
        fireEvent.change(field(), {target: {value: 'Runbooks and drills'}});
        fireEvent.blur(field());

        expect(mockUpdatePage).toHaveBeenCalledTimes(1);

        // The first write lands, so the store's title catches up before the promise
        // the caller is awaiting resolves.
        rerender(content({page: makePage('runbook', 'eng', 'Runbooks')}));
        resolveWrite(PAGE);

        await waitFor(() => expect(mockUpdatePage).toHaveBeenCalledTimes(2));
        expect(mockUpdatePage).toHaveBeenLastCalledWith('eng', 'runbook', {title: 'Runbooks and drills'});
        expect(field()).toHaveValue('Runbooks and drills');
    });

    it('commits on leaving edit mode, which raises no blur', async () => {
        const {rerender} = renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        rerender(content({editing: false}));

        await waitFor(() => expect(mockUpdatePage).toHaveBeenCalledWith('eng', 'runbook', {title: 'Runbooks'}));
    });

    it('does not commit again on leaving edit mode when a blur already wrote', async () => {
        const {rerender} = renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.blur(field());
        await waitFor(() => expect(mockUpdatePage).toHaveBeenCalledTimes(1));

        rerender(content({page: makePage('runbook', 'eng', 'Runbooks'), editing: false}));

        await Promise.resolve();
        expect(mockUpdatePage).toHaveBeenCalledTimes(1);
    });

    it('shows the stored title, not the rejected one, once back in reading mode', async () => {
        mockUpdatePage.mockReturnValue(Promise.reject(new Error('nope')));
        const {rerender} = renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        rerender(content({editing: false}));

        await waitFor(() => expect(mockToastError).toHaveBeenCalled());
        expect(screen.getByRole('heading', {name: 'Runbook'})).toBeInTheDocument();
    });
});

describe('PageContent title buffer', () => {
    beforeEach(() => {
        mockUpdatePage.mockReset().mockReturnValue(Promise.resolve(PAGE));
        mockToastError.mockReset();
    });

    it('follows a title changed elsewhere while nothing is typed', () => {
        const {rerender} = renderContent();

        rerender(content({page: makePage('runbook', 'eng', 'Renamed elsewhere')}));

        expect(field()).toHaveValue('Renamed elsewhere');
    });

    it('keeps typed input when a title arrives from elsewhere', () => {
        const {rerender} = renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        rerender(content({page: makePage('runbook', 'eng', 'Renamed elsewhere')}));

        expect(field()).toHaveValue('Runbooks');
    });

    it('starts over for another routed page, committing what was typed for the last one', async () => {
        const {rerender} = renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        rerender(content({page: makePage('oncall', 'eng', 'On call')}));

        expect(field()).toHaveValue('On call');
        await waitFor(() => expect(mockUpdatePage).toHaveBeenCalledWith('eng', 'runbook', {title: 'Runbooks'}));
    });
});
