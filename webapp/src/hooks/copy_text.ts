// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';
import {copyToClipboard} from 'utils/clipboard';

import {announce} from 'components/readout/readout_store';

const COPIED_TIMEOUT = 2000;

type CopyOptions = {
    announcement?: string;
    timeout?: number;
};

type CopyText = {

    /** True for a moment after a copy, for controls that confirm in place. */
    copied: boolean;
    copy: () => void;
};

// Core's useCopyText, which plugins can't import. Controls own the confirmation:
// core swaps the label and icon rather than raising a toast.
export function useCopyText(text: string, {announcement, timeout = COPIED_TIMEOUT}: CopyOptions = {}): CopyText {
    const [copied, setCopied] = useState(false);
    const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
    const mounted = useRef(true);

    useEffect(() => {
        mounted.current = true;

        return () => {
            mounted.current = false;
            if (timer.current) {
                clearTimeout(timer.current);
            }
        };
    }, []);

    const copy = useCallback(async () => {
        const done = await copyToClipboard(text);
        if (!done || !mounted.current) {
            return;
        }

        if (announcement) {
            announce(announcement);
        }

        if (timer.current) {
            clearTimeout(timer.current);
        }
        setCopied(true);
        timer.current = setTimeout(() => setCopied(false), timeout);
    }, [announcement, text, timeout]);

    return {copied, copy};
}
