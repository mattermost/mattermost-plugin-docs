// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {createContext, useContext, useSyncExternalStore} from 'react';

import {getDocsModalStack, subscribeToDocsModals} from './modal_store';
import type {DocsModalEntry, DocsModalHandle} from './modal_store';

const DocsModalContext = createContext<DocsModalHandle | undefined>(undefined);

/** The handle of the modal the calling component is rendered inside, if any. */
export const useDocsModal = () => useContext(DocsModalContext);

export type DocsModalLayer = {

    /** 0-based position in the stack, so each level can own a paint order. */
    level: number;

    /** How many modals are stacked on top of this one. */
    covered: number;
};

const NOT_STACKED: DocsModalLayer = {level: 0, covered: 0};

// Base UI decides whether a dialog is nested from React context — a `Dialog.Root`
// is nested only when it renders inside another one's subtree (see
// DialogRoot: `nested = Boolean(useDialogRootContext(true))`). This controller
// renders the stack as siblings, so Base UI sees unrelated dialogs and never
// applies its own nesting treatment (`data-nested-dialog-open`,
// `--nested-dialogs`). The stack is what knows the depth, so it supplies it.
const DocsModalLayerContext = createContext<DocsModalLayer>(NOT_STACKED);

/**
 * This modal's place in the stack. Defaults to a lone top-level modal, so a
 * dialog rendered outside the stack behaves as it always did.
 */
export const useDocsModalLayer = () => useContext(DocsModalLayerContext);

type EntryProps = {
    entry: DocsModalEntry;
    layer: DocsModalLayer;
};

const DocsModalEntryMount = ({entry, layer}: EntryProps) => (
    <DocsModalContext.Provider value={entry.handle}>
        <DocsModalLayerContext.Provider value={layer}>
            {entry.render(entry.handle)}
        </DocsModalLayerContext.Provider>
    </DocsModalContext.Provider>
);

/**
 * Renders the stack of modals opened through `openDocsModal`. Mount exactly
 * once, at the Docs root.
 */
const DocsModalController = () => {
    const stack = useSyncExternalStore(subscribeToDocsModals, getDocsModalStack);

    return (
        <>
            {stack.map((entry, index) => (
                <DocsModalEntryMount
                    key={entry.handle.id}
                    entry={entry}
                    layer={{level: index, covered: stack.length - 1 - index}}
                />
            ))}
        </>
    );
};

export default DocsModalController;
