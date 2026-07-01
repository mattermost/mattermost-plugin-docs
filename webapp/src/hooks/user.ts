// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import type {GlobalState} from '@mattermost/types/store';

import {getTeammateNameDisplaySetting} from 'mattermost-redux/selectors/entities/preferences';
import {getCurrentUser} from 'mattermost-redux/selectors/entities/users';
import {displayUsername, getFullName} from 'mattermost-redux/utils/user_utils';

// The current user's greeting name, read from the host's Redux store (the
// current user is platform data, not Docs-domain data, so it comes from
// mattermost-redux rather than the Docs data source). A greeting wants the
// first name, derived via the getFullName formatter (no raw field access);
// when no real name is set it degrades gracefully through displayUsername,
// which falls back to the username (then a generic label) — the same formatters
// playbooks uses (run_playbook_modal.tsx).
export function useCurrentUser(): {name: string} {
    const user = useSelector((state: GlobalState) => getCurrentUser(state));
    const teammateNameDisplay = useSelector((state: GlobalState) => getTeammateNameDisplaySetting(state)) || '';

    const firstName = user ? getFullName(user).trim().split(' ')[0] : '';
    const name = firstName || displayUsername(user, teammateNameDisplay);

    return {name};
}
