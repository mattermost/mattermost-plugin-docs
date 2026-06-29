// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {docsDataSource} from 'data';

// Current team context for scoping Docs data. Backed by the mock source now;
// later wraps the mattermost-redux current-team selectors.
export function useTeamContext(): {teamName: string} {
    return {teamName: docsDataSource.getCurrentTeamName()};
}
