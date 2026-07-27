// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {ClientError} from '@mattermost/client';

import {Client4} from 'mattermost-redux/client';

// Base URL for the Docs plugin REST API. Client4.url is the host-configured
// site URL (including any subpath), so this resolves correctly on subpath-hosted
// instances without extra wiring. Deferred to call time because the host sets
// Client4.url after our bundle loads.
const apiUrl = (): string => `${Client4.url}/plugins/${manifest.id}/api/v1`;

type FetchOptions = {
    method: string;
    body?: string;
    headers?: Record<string, string>;
};

// Single fetch idiom shared by every Docs API call. Client4.getOptions injects
// the session credentials and CSRF header the server expects (it reads the
// platform-supplied Mattermost-User-Id header), so this never hand-rolls auth.
// Server errors are JSON `AppError`s ({message, status_code}); non-OK responses
// are normalized into ClientError from @mattermost/client.
async function doFetch<T>(url: string, options: FetchOptions): Promise<T> {
    const response = await fetch(url, Client4.getOptions(options));

    if (response.ok) {
        // Actions like DELETE return {"status":"OK"}; callers that expect no
        // payload type this as void and ignore it.
        const text = await response.text();
        return (text ? JSON.parse(text) : {}) as T;
    }

    let message = `Received status code ${response.status}`;
    try {
        const data = await response.json();
        message = data.message || message;
    } catch {
        // Non-JSON error body — keep the status-based message.
    }
    throw new ClientError(Client4.url, {message, status_code: response.status, url});
}

export const restGet = <T>(url: string): Promise<T> => doFetch<T>(url, {method: 'GET'});

export const restPost = <T>(url: string, body: unknown): Promise<T> =>
    doFetch<T>(url, {method: 'POST', body: JSON.stringify(body), headers: {'Content-Type': 'application/json'}});

export const restDelete = <T>(url: string): Promise<T> => doFetch<T>(url, {method: 'DELETE'});

type Paginated<T> = {
    items: T[];
    page: number;
    per_page: number;
    has_more: boolean;
};

const PER_PAGE = 100;
const MAX_PAGES = 1000;

// Follows the server's {items, page, per_page, has_more} envelope across pages
// and returns the flattened list. The page cap is a runaway-loop backstop, not
// an expected limit.
export async function listAll<T>(path: (query: string) => string): Promise<T[]> {
    const out: T[] = [];
    for (let page = 0; page < MAX_PAGES; page++) {
        // eslint-disable-next-line no-await-in-loop
        const res = await restGet<Paginated<T>>(path(`page=${page}&per_page=${PER_PAGE}`));
        out.push(...res.items);
        if (!res.has_more) {
            break;
        }
    }
    return out;
}

export {apiUrl};
