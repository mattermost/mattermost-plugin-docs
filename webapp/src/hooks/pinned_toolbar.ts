// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useCallback, useRef} from 'react';

import {savePreferences} from 'mattermost-redux/actions/preferences';
import {get as getPreference} from 'mattermost-redux/selectors/entities/preferences';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

const CATEGORY = 'docs_editor';
const NAME = 'toolbar_pinned';

export const usePinnedToolbar = (): [boolean, () => void] => {
    const dispatch = useAppDispatch();
    const userId = useAppSelector(getCurrentUserId);
    const stored = useAppSelector((state) => getPreference(state, CATEGORY, NAME, 'true'));
    const pinned = stored !== 'false';

    const seen = useRef(pinned);
    const intended = useRef(pinned);
    if (seen.current !== pinned) {
        seen.current = pinned;
        intended.current = pinned;
    }

    const toggle = useCallback(() => {
        const next = !intended.current;
        intended.current = next;

        dispatch(savePreferences(userId, [{
            user_id: userId,
            category: CATEGORY,
            name: NAME,
            value: String(next),
        }]));
    }, [dispatch, userId]);

    return [pinned, toggle];
};
