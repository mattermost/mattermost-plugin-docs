// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page} from '@playwright/test';

import {readJsonOrThrow, requestedWith} from './client';

type GuestAccountsConfig = {
    GuestAccountsSettings?: {Enable?: boolean};
};

export async function ensureGuestAccountsEnabled(page: Page): Promise<boolean> {
    const configResponse = await page.request.get('/api/v4/config', requestedWith);
    const config = await readJsonOrThrow<GuestAccountsConfig>(configResponse, 'Unable to read server config');
    const wasEnabled = config.GuestAccountsSettings?.Enable === true;

    if (!wasEnabled) {
        await setGuestAccountsEnabled(page, true);
    }

    return wasEnabled;
}

/**
 * Demotes a seeded user to a guest, returning the undo for whatever server state this had
 * to change to do it.
 *
 * Demotion is refused unless GuestAccountsSettings.Enable is on, which is server-wide and
 * therefore shared by every spec running against this container. So the demote is attempted
 * first and the config is only touched when the server actually refuses — a server that
 * already allows guests is left alone, and the returned undo is a no-op.
 *
 * Demote the user *after* adding them to the team and the space: demotion converts the
 * memberships that already exist into guest ones rather than granting new access, so a guest
 * seeded in the other order lands outside the space.
 */
export async function demoteToGuest(page: Page, userId: string): Promise<boolean | null> {
    const demote = () => page.request.post(`/api/v4/users/${userId}/demote`, requestedWith);

    const firstAttempt = await demote();
    if (firstAttempt.ok()) {
        return null; // Nothing was changed to make this work, so there is nothing to put back.
    }

    const refusal = await firstAttempt.text();
    if (!refusal.includes('disabled')) {
        throw new Error(`Unable to demote ${userId} to guest: ${firstAttempt.status()} ${refusal}`);
    }

    const configResponse = await page.request.get('/api/v4/config', requestedWith);
    const config = await readJsonOrThrow<GuestAccountsConfig>(configResponse, 'Unable to read server config');
    const wasEnabled = config.GuestAccountsSettings?.Enable === true;

    await setGuestAccountsEnabled(page, true);

    const retry = await demote();
    if (!retry.ok()) {
        await setGuestAccountsEnabled(page, wasEnabled);
        throw new Error(`Unable to demote ${userId} to guest after enabling guest accounts: ${retry.status()} ${await retry.text()}`);
    }

    return wasEnabled;
}

/**
 * Puts GuestAccountsSettings.Enable back to what demoteToGuest reported.
 *
 * Takes a page rather than being a closure demoteToGuest returns: the restore runs in a later
 * hook, by which time the context the demote was made from has been closed, and a request made
 * through a closed context fails with "Target page, context or browser has been closed".
 */
export async function setGuestAccountsEnabled(page: Page, enable: boolean) {
    const response = await page.request.put('/api/v4/config/patch', {
        ...requestedWith,
        data: {GuestAccountsSettings: {Enable: enable}},
    });

    if (!response.ok()) {
        throw new Error(`Unable to set GuestAccountsSettings.Enable=${enable}: ${response.status()} ${await response.text()}`);
    }
}
