// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeDraft, makePage} from 'store/test_fixtures';

import PageContent from './page_content';

import {renderWithContext} from '../../../../tests/react_testing_utils';

const mockUpdatePage = jest.fn();
const mockSaveDraft = jest.fn();
const mockToastError = jest.fn();

jest.mock('store/actions', () => ({
    updatePage: (...args: unknown[]) => () => mockUpdatePage(...args),
    saveDraft: (...args: unknown[]) => () => mockSaveDraft(...args),
}));
jest.mock('components/toast', () => ({toast: {error: (...args: unknown[]) => mockToastError(...args)}}));
jest.mock('hooks/members', () => ({useUserProfile: () => undefined}));

const PAGE = makePage('runbook', 'eng', 'Runbook');

// What creating a page stores, since the server has no empty-title representation.
const UNNAMED_PAGE = makePage('fresh', 'eng', 'Untitled');

const content = (props: Partial<React.ComponentProps<typeof PageContent>> = {}) => (
    <PageContent
        page={PAGE}
        editing={true}
        {...props}
    />
);

const renderContent = () => renderWithContext(content());

const field = () => screen.getByRole('textbox', {name: 'Page title'});

const noWrites = () => {
    expect(mockSaveDraft).not.toHaveBeenCalled();
    expect(mockUpdatePage).not.toHaveBeenCalled();
};

beforeEach(() => {
    mockUpdatePage.mockReset().mockReturnValue(Promise.resolve(PAGE));
    mockSaveDraft.mockReset().mockReturnValue(Promise.resolve(makeDraft('runbook', 'eng', 'Runbook')));
    mockToastError.mockReset();
});

describe('PageContent title commit', () => {
    // The whole point of the placeholder treatment: a page nobody has named yet
    // opens with an empty field, so typing a name doesn't start with clearing one.
    it('presents an unnamed page as an empty field, not the literal placeholder', () => {
        renderWithContext(content({page: UNNAMED_PAGE}));

        expect(field()).toHaveValue('');
        expect(field()).toHaveAttribute('placeholder', 'Untitled');
    });

    it('writes a name typed over the placeholder', async () => {
        renderWithContext(content({page: UNNAMED_PAGE}));

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Enter'});

        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledWith('eng', 'fresh', {title: 'Runbooks'}));
    });

    // Leaving a fresh page untouched must not look like a rename back to the same
    // value; the buffer is empty while the stored title isn't.
    it('does not write when an unnamed page is left unnamed', () => {
        renderWithContext(content({page: UNNAMED_PAGE}));

        fireEvent.keyDown(field(), {key: 'Enter'});

        noWrites();
    });

    it('does not write while typing', () => {
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});

        noWrites();
    });

    it('writes the trimmed title on Enter', async () => {
        renderContent();

        fireEvent.change(field(), {target: {value: '  Runbooks  '}});
        fireEvent.keyDown(field(), {key: 'Enter'});

        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledWith('eng', 'runbook', {title: 'Runbooks'}));
    });

    it('writes on blur', async () => {
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.blur(field());

        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledWith('eng', 'runbook', {title: 'Runbooks'}));
    });

    it('does not write when the title is unchanged', () => {
        renderContent();

        fireEvent.keyDown(field(), {key: 'Enter'});

        noWrites();
    });

    // The server rejects an empty title, so clearing the field returns the page to
    // unnamed by storing the placeholder — and the field stays empty, since that is
    // how unnamed is presented.
    it('stores the untitled placeholder when the field is emptied', async () => {
        renderContent();

        fireEvent.change(field(), {target: {value: ''}});
        fireEvent.keyDown(field(), {key: 'Enter'});

        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledWith('eng', 'runbook', {title: 'Untitled'}));
        expect(field()).toHaveValue('');
    });

    it('reverts on Escape without writing', () => {
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Escape'});

        expect(field()).toHaveValue('Runbook');
        noWrites();
    });

    it('keeps the typed title and reports a failed write', async () => {
        mockSaveDraft.mockReturnValue(Promise.reject(new Error('nope')));
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Enter'});

        await waitFor(() => expect(mockToastError).toHaveBeenCalled());
        expect(field()).toHaveValue('Runbooks');
    });

    it('does not fire a second write while the first is still in flight', async () => {
        let resolveWrite: () => void = () => {};
        mockSaveDraft.mockReturnValue(new Promise((resolve) => {
            resolveWrite = () => resolve(makeDraft('runbook', 'eng', 'Runbook'));
        }));
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Enter'});
        fireEvent.blur(field());

        expect(mockSaveDraft).toHaveBeenCalledTimes(1);

        resolveWrite();
        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1));
    });

    it('sends what was typed while the first write was in flight, once it settles', async () => {
        let resolveWrite: () => void = () => {};
        mockSaveDraft.mockReturnValueOnce(new Promise((resolve) => {
            resolveWrite = () => resolve(makeDraft('runbook', 'eng', 'Runbook'));
        }));
        const {rerender} = renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Enter'});
        fireEvent.change(field(), {target: {value: 'Runbooks and drills'}});
        fireEvent.blur(field());

        expect(mockSaveDraft).toHaveBeenCalledTimes(1);

        // The first write lands, so the store's title catches up before the promise
        // the caller is awaiting resolves.
        rerender(content({page: makePage('runbook', 'eng', 'Runbooks')}));
        resolveWrite();

        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(2));
        expect(mockSaveDraft).toHaveBeenLastCalledWith('eng', 'runbook', {title: 'Runbooks and drills'});
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
        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledTimes(1));

        rerender(content({page: makePage('runbook', 'eng', 'Runbooks'), editing: false}));

        await Promise.resolve();
        expect(mockSaveDraft).toHaveBeenCalledTimes(1);
        expect(mockUpdatePage).not.toHaveBeenCalled();
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

describe('PageContent title during an edit session', () => {
    it('drafts a rename rather than patching the published page', async () => {
        renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        fireEvent.keyDown(field(), {key: 'Enter'});

        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalled());
        expect(mockUpdatePage).not.toHaveBeenCalled();
    });

    it('drafts a rename made after leaving the field, while a draft is open', async () => {
        const {rerender} = renderContent();

        fireEvent.change(field(), {target: {value: 'Runbooks'}});
        rerender(content({draft: makeDraft('runbook', 'eng', 'Runbook'), editing: false}));

        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledWith('eng', 'runbook', {title: 'Runbooks'}));
        expect(mockUpdatePage).not.toHaveBeenCalled();
    });

    it('shows the drafted title, not the published one', () => {
        renderWithContext(content({draft: makeDraft('runbook', 'eng', 'Drafted name')}));

        expect(field()).toHaveValue('Drafted name');
    });

    it('shows the published title to a reader', () => {
        renderWithContext(content({draft: makeDraft('runbook', 'eng', 'Drafted name'), editing: false}));

        expect(screen.getByRole('heading', {name: 'Runbook'})).toBeInTheDocument();
    });
});

describe('PageContent title buffer', () => {
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
        await waitFor(() => expect(mockSaveDraft).toHaveBeenCalledWith('eng', 'runbook', {title: 'Runbooks'}));
    });
});
