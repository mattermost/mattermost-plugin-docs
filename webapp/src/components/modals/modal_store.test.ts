// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    closeAllDocsModals,
    closeDocsModal,
    getDocsModalStack,
    openDocsModal,
    subscribeToDocsModals,
} from './modal_store';

describe('modal store', () => {
    afterEach(() => {
        closeAllDocsModals();
    });

    it('stacks modals in the order they were opened', () => {
        const first = openDocsModal('one');
        const second = openDocsModal('two');

        expect(getDocsModalStack().map((entry) => entry.handle.id)).toEqual([first.id, second.id]);
    });

    it('pops only the modal a handle refers to, whatever its position', () => {
        const first = openDocsModal('one');
        const second = openDocsModal('two');

        first.close();

        expect(getDocsModalStack().map((entry) => entry.handle.id)).toEqual([second.id]);
    });

    it('wraps a plain node so callers can always render through the entry', () => {
        const handle = openDocsModal('content');

        expect(getDocsModalStack()[0].render(handle)).toBe('content');
    });

    it('passes the handle to a render function', () => {
        const render = jest.fn(() => 'rendered');
        const handle = openDocsModal(render);

        getDocsModalStack()[0].render(handle);

        expect(render).toHaveBeenCalledWith(handle);
    });

    it('notifies subscribers on open and close, and stops after unsubscribe', () => {
        const listener = jest.fn();
        const unsubscribe = subscribeToDocsModals(listener);

        const handle = openDocsModal('one');
        expect(listener).toHaveBeenCalledTimes(1);

        handle.close();
        expect(listener).toHaveBeenCalledTimes(2);

        unsubscribe();
        openDocsModal('two');
        expect(listener).toHaveBeenCalledTimes(2);
    });

    // Guards against a stale handle (a modal already closed another way) waking
    // every subscriber for a no-op.
    it('does not notify when closing an id that is not on the stack', () => {
        const listener = jest.fn();
        const unsubscribe = subscribeToDocsModals(listener);

        closeDocsModal('docs-modal-nope');
        expect(listener).not.toHaveBeenCalled();

        closeAllDocsModals();
        expect(listener).not.toHaveBeenCalled();

        unsubscribe();
    });

    it('clears the whole stack at once', () => {
        openDocsModal('one');
        openDocsModal('two');

        closeAllDocsModals();

        expect(getDocsModalStack()).toEqual([]);
    });
});
