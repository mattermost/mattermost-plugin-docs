// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useMemo} from 'react';
import {useSelector} from 'react-redux';

import type {GlobalState} from '@mattermost/types/store';

import {getCurrentTeam, getMyTeams} from 'mattermost-redux/selectors/entities/teams';

type TeamContext = {
    id: string;
    name: string;
    displayName: string;
    description: string;
};

export function useTeamContext(): TeamContext {
    const team = useSelector((state: GlobalState) => getCurrentTeam(state));

    return {
        id: team?.id ?? '',
        name: team?.name ?? '',
        displayName: team?.display_name ?? '',
        description: team?.description ?? '',
    };
}

// Maps team id → URL name for the user's teams. The cross-team switcher uses it
// to route a result to its own team's URL (team names, not ids, are in the URL).
export function useTeamNamesById(): Map<string, string> {
    const teams = useSelector(getMyTeams);
    return useMemo(() => new Map(teams.map((team) => [team.id, team.name])), [teams]);
}
