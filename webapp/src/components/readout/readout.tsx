// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useSyncExternalStore} from 'react';

import styles from './readout.module.scss';
import {clearReadout, getReadoutState, subscribeToReadout} from './readout_store';

// Core clears after 2s so a stale message isn't re-read when focus returns to the
// region (webapp/channels/src/components/readout).
const CLEAR_DELAY = 2000;

/**
 * The Docs live region: one per product, mounted at the Docs root. Announces
 * whatever `announce()` last passed, then empties itself.
 */
const Readout = () => {
    const {message, nonce} = useSyncExternalStore(subscribeToReadout, getReadoutState);

    // Keyed on the nonce so each announcement resets its own timer: a newer message
    // can't be wiped early by the previous one's pending clear.
    useEffect(() => {
        if (!message) {
            return undefined;
        }
        const timeout = setTimeout(() => clearReadout(nonce), CLEAR_DELAY);
        return () => clearTimeout(timeout);
    }, [message, nonce]);

    return (
        <div
            className={styles.readout}
            role='status'
            aria-live='polite'
            aria-atomic='true'
        >
            <span key={nonce}>{message}</span>
        </div>
    );
};

export default Readout;
