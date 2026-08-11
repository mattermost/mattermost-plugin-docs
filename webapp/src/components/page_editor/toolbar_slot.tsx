// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {createContext, useContext, useMemo, useState} from 'react';

// The pinned toolbar belongs above the page heading and outside the scrolling
// content, while the editor that owns it renders below both. The slot marks where
// it goes so the editor can portal it there.
type Slot = {
    element: HTMLElement | null;
    setElement: (element: HTMLElement | null) => void;
};

const SlotContext = createContext<Slot>({element: null, setElement: () => undefined});

export const ToolbarSlotProvider = ({children}: {children: React.ReactNode}) => {
    const [element, setElement] = useState<HTMLElement | null>(null);
    const value = useMemo(() => ({element, setElement}), [element]);

    return <SlotContext.Provider value={value}>{children}</SlotContext.Provider>;
};

export const ToolbarSlot = () => {
    const {setElement} = useContext(SlotContext);

    return <div ref={setElement}/>;
};

export const useToolbarSlot = (): HTMLElement | null => useContext(SlotContext).element;
