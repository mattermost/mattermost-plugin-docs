// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';

const STORAGE_KEY = 'docs_toolbar_pinned';

const readStored = (): boolean => {
    try {
        return window.localStorage.getItem(STORAGE_KEY) !== 'false';
    } catch {
        return true;
    }
};

const writeStored = (pinned: boolean): boolean => {
    try {
        window.localStorage.setItem(STORAGE_KEY, String(pinned));
        return true;
    } catch {
        return false;
    }
};

export const usePinnedToolbar = (): [boolean, () => void] => {
    const [pinned, setPinned] = useState(readStored);

    const toggle = useCallback(() => setPinned((prev) => !prev), []);

    const firstRender = useRef(true);
    useEffect(() => {
        if (firstRender.current) {
            firstRender.current = false;
            return;
        }
        writeStored(pinned);
    }, [pinned]);

    return [pinned, toggle];
};
