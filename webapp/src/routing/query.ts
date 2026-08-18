// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * The current location with its query edited in place: `mutate` receives the
 * existing params, so a caller changing one key leaves the others alone. Docs
 * carries several independent view states in the query (edit mode, the open
 * right-hand panel, fullscreen), and rebuilding the query from scratch would drop
 * whichever ones the caller didn't know about.
 */
export const withQuery = (pathname: string, search: string, mutate: (params: URLSearchParams) => void): string => {
    const params = new URLSearchParams(search);
    mutate(params);

    const query = params.toString();
    return query ? `${pathname}?${query}` : pathname;
};
