// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback} from 'react';
import {useHistory, useLocation} from 'react-router-dom';
import {FULLSCREEN_QUERY} from 'routing/paths';
import {withQuery} from 'routing/query';

/**
 * Fullscreen: the spaces sidebar is hidden and the page has the window. Read from
 * the URL, like edit mode and the right-hand panel — the control that toggles it
 * sits in the page header while the sidebar it hides belongs to the product root,
 * and the two share nothing else.
 *
 * Toggling replaces the history entry rather than pushing one, so Back leaves the
 * page instead of stepping back through however many times the reader resized their
 * view.
 */
export function useFullscreen() {
    const history = useHistory();
    const {pathname, search} = useLocation();

    const fullscreen = new URLSearchParams(search).get(FULLSCREEN_QUERY) === '1';

    const toggleFullscreen = useCallback(() => {
        history.replace(withQuery(pathname, search, (params) => {
            if (params.get(FULLSCREEN_QUERY) === '1') {
                params.delete(FULLSCREEN_QUERY);
            } else {
                params.set(FULLSCREEN_QUERY, '1');
            }
        }));
    }, [history, pathname, search]);

    return {fullscreen, toggleFullscreen};
}
