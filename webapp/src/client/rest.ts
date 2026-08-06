// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {ClientError} from '@mattermost/client';

import {Client4} from 'mattermost-redux/client';

// Shared plumbing for calls to the Docs plugin REST API.
//
// Deliberately a subset: the Spaces UI branch (PR #12) carries a fuller version
// of this module at the same path with the same exported names, adding the verbs
// and the paginated listAll this permissions surface does not need. Matching its
// names means the two resolve to "take the superset" when the branches meet,
// rather than leaving a second REST client behind.

// Client4.url is the host-configured site URL including any subpath, so this
// resolves on subpath-hosted instances without extra wiring. Deferred to call
// time because the host sets Client4.url after our bundle loads.
export const apiUrl = (): string => `${Client4.url}/plugins/${manifest.id}/api/v1`;

type FetchOptions = {
    method: string;
    body?: string;
    headers?: Record<string, string>;
};

/**
 * A failed Docs API call. Extends ClientError so callers can match on
 * `instanceof ClientError` and `status_code`, and adds what an AppError body
 * carries that ClientError drops: the raw payload and a plain `status`.
 */
export class RestError extends ClientError {
    status: number;
    body: unknown;

    constructor(url: string, status: number, message: string, body: unknown, serverErrorId?: string) {
        super(Client4.url, {message, status_code: status, url, server_error_id: serverErrorId});
        this.name = 'RestError';
        this.status = status;
        this.body = body;
    }
}

// The single fetch idiom every Docs API call goes through. Client4.getOptions
// injects the session credentials and CSRF header the server expects, so this
// never hand-rolls auth. Server errors are JSON AppErrors ({message, id,
// status_code}); a non-OK response becomes a RestError carrying all three.
async function request<T>(url: string, options: FetchOptions): Promise<T> {
    const response = await fetch(url, Client4.getOptions(options));

    if (response.ok) {
        // Actions like DELETE answer {"status":"OK"}; callers expecting no
        // payload type this as void and ignore it.
        const text = await response.text();
        return (text ? JSON.parse(text) : {}) as T;
    }

    let message = `Received status code ${response.status}`;
    let body: unknown;
    let serverErrorId: string | undefined;
    try {
        body = await response.json();
        const data = body as {message?: string; id?: string};
        message = data.message || message;
        serverErrorId = data.id;
    } catch {
        // Non-JSON error body — keep the status-based message.
    }
    throw new RestError(url, response.status, message, body, serverErrorId);
}

export const restGet = <T>(url: string): Promise<T> =>
    request<T>(url, {method: 'GET'});

export const restPut = <T>(url: string, body: unknown): Promise<T> =>
    request<T>(url, {method: 'PUT', body: JSON.stringify(body), headers: {'Content-Type': 'application/json'}});
