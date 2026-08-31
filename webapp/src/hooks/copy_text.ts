// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';
import {copyToClipboard} from 'utils/clipboard';

const COPIED_TIMEOUT = 2000;

type CopyText = {

    /** True for a moment after a copy, for controls that confirm in place. */
    copied: boolean;
    copy: () => void;
};

// Core's useCopyText, which plugins can't import. Controls own the confirmation:
// core swaps the label and icon rather than raising a toast.
export function useCopyText(text: string, timeout: number = COPIED_TIMEOUT): CopyText {
    const [copied, setCopied] = useState(false);
    const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(() => () => {
        if (timer.current) {
            clearTimeout(timer.current);
        }
    }, []);

    const copy = useCallback(() => {
        copyToClipboard(text);

        if (timer.current) {
            clearTimeout(timer.current);
        }
        setCopied(true);
        timer.current = setTimeout(() => setCopied(false), timeout);
    }, [text, timeout]);

    return {copied, copy};
}
