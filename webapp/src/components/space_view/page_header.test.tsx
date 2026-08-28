// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
import manifest from 'manifest';
import React from 'react';
import {legacy_createStore as createStore} from 'redux';
import type {UnknownAction} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import {SpaceTypes} from 'store/action_types';
import docsReducer from 'store/reducer';
import {makeDraft, makePage, makeSpace} from 'store/test_fixtures';
import type {DocsPluginState} from 'store/types';

import {toast} from 'components/toast';

import type {Permission} from 'types/permissions';

import PageHeader from './page_header';

import {makeTestState, renderWithContext} from '../../../tests/react_testing_utils';

jest.mock('webapp_globals', () => ({Timestamp: () => null}));
jest.mock('hooks/pages', () => ({useCreateRootPage: () => jest.fn()}));

// Stubbed at the hook boundary: mattermost-redux's preferences actions are
// published as ESM that jest doesn't transform.
jest.mock('hooks/favorites', () => ({
    useIsFavorite: () => false,
    useToggleFavorite: () => jest.fn(),
}));

jest.mock('components/toast', () => ({toast: {error: jest.fn()}}));

const SPACE = makeSpace('eng', 'Engineering');
const PAGE = makePage('runbook', 'eng', 'Runbook');

const PAGE_URL = '/myteam/spaces/eng/runbook';

const renderHeader = (props: Partial<React.ComponentProps<typeof PageHeader>> = {}, route = PAGE_URL, space = SPACE) =>
    renderWithContext(
        <PageHeader
            space={space}
            page={PAGE}
            treeOpen={false}
            editing={false}
            commentsOpen={false}
            onTogglePages={jest.fn()}
            onToggleComments={jest.fn()}
            onToggleEdit={jest.fn()}
            onPublish={jest.fn()}
            {...props}
        />,
        {route, state: {docs: {spaces: {[space.id]: space}}}},
    );

// A space the server has resolved without edit_page: a member of a space whose default grants
// reading and creating but not editing what is already published.
const NO_EDIT_SPACE = {...SPACE, permissions: ['read_page', 'create_page'] as Permission[]};

// The mirror: a reader who may edit what is already published but may not add a page. Drafting is
// admitted by either permission (RequireSpaceDraftWrite), so this caller can hold a draft the
// publish gate will then refuse.
const NO_CREATE_SPACE = {...SPACE, permissions: ['read_page', 'edit_page'] as Permission[]};

// A store running the real Docs reducer, so a dispatched RECEIVED_SPACES (the action a
// re-resolved permission set arrives on) actually lands, exercising the same selector recompute
// the header sees in the app rather than a prop change the test drives directly.
const pluginKey = 'plugins-' + manifest.id;
const makeLiveStore = (space = SPACE) => {
    const initial = makeTestState({docs: {spaces: {[space.id]: space}}});

    return createStore((state: GlobalState = initial, action: UnknownAction): GlobalState => ({
        ...state,
        [pluginKey]: docsReducer((state as unknown as Record<string, DocsPluginState>)[pluginKey], action),
    } as unknown as GlobalState));
};

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

    it('enters edit mode on e while reading', () => {
        const onToggleEdit = jest.fn();
        renderHeader({onToggleEdit});

        fireEvent.keyDown(document, {key: 'e', code: 'KeyE'});

        expect(onToggleEdit).toHaveBeenCalledTimes(1);
    });

    it('does not enter edit mode on e while already editing', () => {
        const onToggleEdit = jest.fn();
        renderHeader({editing: true, onToggleEdit});

        fireEvent.keyDown(document, {key: 'e', code: 'KeyE'});

        expect(onToggleEdit).not.toHaveBeenCalled();
    });

    it('does not enter edit mode on e on the space home', () => {
        const onToggleEdit = jest.fn();
        renderHeader({page: undefined, onToggleEdit});

        fireEvent.keyDown(document, {key: 'e', code: 'KeyE'});

        expect(onToggleEdit).not.toHaveBeenCalled();
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

    it('publishes on click', async () => {
        const onPublish = jest.fn();
        renderHeader({page: undefined, draft: DRAFT, editing: true, onPublish});

        fireEvent.click(screen.getByRole('button', {name: 'Publish'}));

        await waitFor(() => expect(onPublish).toHaveBeenCalled());
    });

    it('publishes on Cmd/Ctrl+Enter while an unpublished page is open', async () => {
        const onPublish = jest.fn();
        renderHeader({page: undefined, draft: DRAFT, onPublish});

        fireEvent.keyDown(document, {key: 'Enter', code: 'Enter', metaKey: true, ctrlKey: true});

        await waitFor(() => expect(onPublish).toHaveBeenCalled());
    });

    it('updates on Cmd/Ctrl+Enter while editing unpublished changes', async () => {
        const onPublish = jest.fn();
        renderHeader({draft: DRAFT, editing: true, onPublish});

        fireEvent.keyDown(document, {key: 'Enter', code: 'Enter', metaKey: true, ctrlKey: true});

        await waitFor(() => expect(onPublish).toHaveBeenCalled());
    });

    it('does not update on Cmd/Ctrl+Enter when there are no unpublished changes', () => {
        const onPublish = jest.fn();
        renderHeader({editing: true, onPublish});

        fireEvent.keyDown(document, {key: 'Enter', code: 'Enter', metaKey: true, ctrlKey: true});

        expect(onPublish).not.toHaveBeenCalled();
    });
});

describe('PageHeader comments control', () => {
    it('reports a click on the control', () => {
        const onToggleComments = jest.fn();
        renderHeader({onToggleComments});

        fireEvent.click(screen.getByRole('button', {name: 'Comments'}));

        expect(onToggleComments).toHaveBeenCalledTimes(1);
    });

    // The panel it opens is elsewhere on screen, so the control carries whether it
    // is showing rather than leaving that to be inferred.
    it('reports the open panel', () => {
        renderHeader({commentsOpen: true});

        expect(screen.getByRole('button', {name: 'Comments'})).toHaveAttribute('aria-pressed', 'true');
    });

    it('offers no comments control on the space home, where no page is routed', () => {
        renderHeader({page: undefined});

        expect(screen.queryByRole('button', {name: 'Comments'})).not.toBeInTheDocument();
    });
});

// The control lives here while the sidebar it hides belongs to the product root, so
// the mode travels in the URL — these assert this end of it.
describe('PageHeader fullscreen control', () => {
    it('offers fullscreen while windowed', () => {
        renderHeader();

        expect(screen.getByRole('button', {name: 'Expand'})).toHaveAttribute('aria-pressed', 'false');
    });

    it('offers the way out while fullscreen', () => {
        renderHeader({}, `${PAGE_URL}?fs=1`);

        const control = screen.getByRole('button', {name: 'Exit fullscreen'});
        expect(control).toHaveAttribute('aria-pressed', 'true');
        expect(screen.queryByRole('button', {name: 'Expand'})).not.toBeInTheDocument();
    });

    it('enters fullscreen without pushing a history entry', () => {
        const {history} = renderHeader();
        const before = history.length;

        fireEvent.click(screen.getByRole('button', {name: 'Expand'}));

        expect(history.location.search).toBe('?fs=1');
        expect(history.length).toBe(before);
    });
});

// The server splits the two publish gates: a new page takes create_page, publishing over a live one
// takes edit_page. These assert the header offers only what the caller's resolved set allows, so a
// member without edit_page is never walked into a write the server will refuse.
describe('PageHeader edit gating', () => {
    const DRAFT = makeDraft('runbook', 'eng', 'Runbook');

    it('withholds Edit on a published page when the resolved set omits edit_page', () => {
        renderHeader({}, PAGE_URL, NO_EDIT_SPACE);

        expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument();
    });

    it('withholds Update from a caller who cannot edit', () => {
        renderHeader({draft: DRAFT, editing: true}, PAGE_URL, NO_EDIT_SPACE);

        expect(screen.queryByRole('button', {name: 'Update'})).not.toBeInTheDocument();
    });

    // Publishing an unpublished page for the first time is gated on create_page, which this caller
    // holds — so their own draft stays editable and publishable.
    it('still offers Publish and Edit on the caller\'s own unpublished page', () => {
        renderHeader({page: undefined, draft: DRAFT}, PAGE_URL, NO_EDIT_SPACE);

        expect(screen.getByRole('button', {name: 'Publish'})).toBeEnabled();
        expect(screen.getByRole('button', {name: 'Edit'})).toBeInTheDocument();
    });

    it('withholds Publish and Edit on an unpublished page when the resolved set omits create_page', () => {
        renderHeader({page: undefined, draft: DRAFT}, PAGE_URL, NO_CREATE_SPACE);

        expect(screen.queryByRole('button', {name: 'Publish'})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument();
    });

    it('does not publish on Cmd/Ctrl+Enter when the caller cannot create', () => {
        const onPublish = jest.fn();
        renderHeader({page: undefined, draft: DRAFT, onPublish}, PAGE_URL, NO_CREATE_SPACE);

        fireEvent.keyDown(document, {key: 'Enter', metaKey: true});
        fireEvent.keyDown(document, {key: 'Enter', ctrlKey: true});

        expect(onPublish).not.toHaveBeenCalled();
    });

    it('does not enter edit on the `e` shortcut when the caller cannot edit', () => {
        const onToggleEdit = jest.fn();
        renderHeader({onToggleEdit}, PAGE_URL, NO_EDIT_SPACE);

        fireEvent.keyDown(document, {key: 'e'});

        expect(onToggleEdit).not.toHaveBeenCalled();
    });

    it('leaves Close reachable if a caller who cannot edit is already editing', () => {
        renderHeader({editing: true}, PAGE_URL, NO_EDIT_SPACE);

        expect(screen.getByRole('button', {name: 'Close'})).toBeInTheDocument();
    });
});

// Update disappears the moment canEditPage flips false (canCommit depends on it), silently —
// this notice is what tells a caller mid-edit why the control it was about to press just vanished.
describe('PageHeader edit access revoked mid-edit', () => {
    const EDITABLE_SPACE = {...SPACE, permissions: ['read_page', 'create_page', 'edit_page'] as Permission[]};

    beforeEach(() => {
        jest.clearAllMocks();
    });

    const renderLive = (space: typeof SPACE, editing: boolean) => {
        const store = makeLiveStore(space);
        renderWithContext(
            <PageHeader
                space={space}
                page={PAGE}
                treeOpen={false}
                editing={editing}
                commentsOpen={false}
                onTogglePages={jest.fn()}
                onToggleComments={jest.fn()}
                onToggleEdit={jest.fn()}
                onPublish={jest.fn()}
            />,
            {route: PAGE_URL, store},
        );
        return store;
    };

    it('gives no notice on mount when the caller already cannot edit', () => {
        renderLive(NO_EDIT_SPACE, true);

        expect(toast.error).not.toHaveBeenCalled();
    });

    it('notices exactly once when the grant is revoked while editing', () => {
        const store = renderLive(EDITABLE_SPACE, true);

        act(() => {
            store.dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces: [NO_EDIT_SPACE]});
        });
        expect(toast.error).toHaveBeenCalledTimes(1);

        // Nothing has changed the second time around; the transition already fired.
        act(() => {
            store.dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces: [NO_EDIT_SPACE]});
        });
        expect(toast.error).toHaveBeenCalledTimes(1);
    });

    it('gives no notice when the grant is revoked while only reading', () => {
        const store = renderLive(EDITABLE_SPACE, false);

        act(() => {
            store.dispatch({type: SpaceTypes.RECEIVED_SPACES, spaces: [NO_EDIT_SPACE]});
        });

        expect(toast.error).not.toHaveBeenCalled();
    });
});
