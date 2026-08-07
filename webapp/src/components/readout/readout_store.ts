// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type ReadoutState = {
    message: string;

    // Bumped on every announcement. `<Readout/>` keys its text node on this, so an
    // identical message still replaces the node — assistive tech announces DOM
    // mutations inside a live region, and re-rendering the same string is not one.
    // Clearing state and re-setting it in the same tick would not work: React
    // batches both updates into one commit, so the blank never reaches the DOM.
    nonce: number;
};

// Ported from core's readout (webapp/channels/src/components/readout + the
// SET_READOUT/CLEAR_READOUT reducer): one live region for the whole product, fed
// imperatively, cleared shortly after. Core carries the message in Redux; this
// uses the same module-store transport as the Docs toast and modal controllers so
// non-component code can announce without a dispatch.
let state: ReadoutState = {message: '', nonce: 0};

const listeners = new Set<() => void>();

// The snapshot identity only changes when the state does, which is what
// useSyncExternalStore requires.
const setState = (next: ReadoutState) => {
    state = next;
    listeners.forEach((listener) => listener());
};

export const subscribeToReadout = (listener: () => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

export const getReadoutState = (): ReadoutState => state;

/** The text currently in the live region. Exported for tests. */
export const getReadoutMessage = (): string => state.message;

export const clearReadout = () => {
    if (state.message !== '') {
        setState({message: '', nonce: state.nonce + 1});
    }
};

/**
 * Announces `text` to screen readers via the Docs live region, without moving
 * focus. Use for outcomes that are only conveyed visually — a page moved, a list
 * reordered — where a toast would be too heavy.
 *
 * Announcing the same text twice in a row announces twice.
 */
export const announce = (text: string) => {
    setState({message: text, nonce: state.nonce + 1});
};
