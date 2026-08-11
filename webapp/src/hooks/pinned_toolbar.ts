// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch, useAppSelector} from 'hooks/redux';
import {useCallback} from 'react';

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

    const toggle = useCallback(() => {
        dispatch(savePreferences(userId, [{
            user_id: userId,
            category: CATEGORY,
            name: NAME,
            value: String(!pinned),
        }]));
    }, [dispatch, userId, pinned]);

    return [pinned, toggle];
};
