// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ReactNode} from 'react';

export type DocsModalHandle = {
    id: string;
    close: () => void;
};

export type DocsModalRender = (handle: DocsModalHandle) => ReactNode;

export type DocsModalEntry = {
    handle: DocsModalHandle;
    render: DocsModalRender;
};

let stack: DocsModalEntry[] = [];
let nextId = 0;

const listeners = new Set<() => void>();

const setStack = (next: DocsModalEntry[]) => {
    stack = next;
    listeners.forEach((listener) => listener());
};

export const subscribeToDocsModals = (listener: () => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

export const getDocsModalStack = () => stack;

export const closeDocsModal = (id: string) => {
    if (stack.some((entry) => entry.handle.id === id)) {
        setStack(stack.filter((entry) => entry.handle.id !== id));
    }
};

/**
 * Opens a Docs modal from anywhere — no hook, no context, and no mount point at
 * the callsite: `<DocsModalController/>` at the Docs root renders it. Opening from
 * within an open modal stacks on top of it, to any depth.
 *
 * The content owns its own dialog surface (e.g. `GenericModal`/`ConfirmModal`,
 * both Base UI `Dialog`-based), so nesting, focus management and backdrops are
 * handled by Base UI's dialog stacking rather than duplicated here. Pass a
 * render function to receive the handle (wire its `close` to the content's
 * `onClose`), or a ready-made element when the content closes itself.
 *
 * @returns a handle whose `close()` pops this modal off the stack.
 */
export const openDocsModal = (content: ReactNode | DocsModalRender): DocsModalHandle => {
    const id = `docs-modal-${nextId++}`;
    const handle: DocsModalHandle = {id, close: () => closeDocsModal(id)};
    const render: DocsModalRender = typeof content === 'function' ? content : () => content;

    setStack([...stack, {handle, render}]);

    return handle;
};

export const closeAllDocsModals = () => {
    if (stack.length > 0) {
        setStack([]);
    }
};
