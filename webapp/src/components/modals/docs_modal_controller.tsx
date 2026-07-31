// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {createContext, useContext, useSyncExternalStore} from 'react';

import {getDocsModalStack, subscribeToDocsModals} from './modal_store';
import type {DocsModalEntry, DocsModalHandle} from './modal_store';

const DocsModalContext = createContext<DocsModalHandle | undefined>(undefined);

/** The handle of the modal the calling component is rendered inside, if any. */
export const useDocsModal = () => useContext(DocsModalContext);

type EntryProps = {
    entry: DocsModalEntry;
};

const DocsModalEntryMount = ({entry}: EntryProps) => (
    <DocsModalContext.Provider value={entry.handle}>
        {entry.render(entry.handle)}
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
            {stack.map((entry) => (
                <DocsModalEntryMount
                    key={entry.handle.id}
                    entry={entry}
                />
            ))}
        </>
    );
};

export default DocsModalController;
