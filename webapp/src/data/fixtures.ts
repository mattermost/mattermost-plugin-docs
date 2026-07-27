// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space} from 'types/docs';

// Design-time fixtures behind the mock data source. The real Docs API replaces
// these once the server contract exists; only the mock source and the store's
// recent-docs selectors read them directly.

function space(id: string, title: string, icon: string): Space {
    return {
        id,
        team_id: '',
        creator_id: '',
        title,
        icon,
        props: {},
        create_at: 0,
        update_at: 0,
        delete_at: 0,
        sort_order: 0,
    };
}

function page(id: string, title: string, spaceId: string): Page {
    return {
        id,
        space_id: spaceId,
        parent_id: '',
        type: 'page',
        title,
        body: '',
        sort_order: 0,
        create_at: 0,
        update_at: 0,
        edit_at: 0,
        delete_at: 0,
    };
}

export const spaces: Space[] = [
    space('project-avalanche', 'Project Avalanche', '✈️'),
    space('contributor-wiki', 'Contributor Wiki', '👨🏻‍💻'),
    space('developers', 'Developers', '⌨️'),
    space('release-discussion', 'Release Discussion', '🚀'),
    space('incident-handbook', 'Incident Handbook', '📕'),
    space('security-incident-handbook', 'Security Incident Handbook', '🛡️'),
    space('product-support', 'Product Support', '🖐️'),
];

export const pages: Page[] = [
    page('operational-procedures', 'Operational Procedures', 'project-avalanche'),
    page('overview-flight-comms', 'Overview of Flight Communication Protocols', 'project-avalanche'),
    page('communication-protocols', 'Communication Protocols', 'project-avalanche'),
];

export const recentSpaceIds = ['project-avalanche'];
export const recentPageIds = ['operational-procedures', 'overview-flight-comms', 'communication-protocols'];

// Recently-viewed-spaces summaries for the Home listing (page counts and
// last-viewed timestamps are mocked until the server provides them; the client
// formats the timestamp into a relative label at render).
const MINUTE = 60 * 1000;
const DAY = 24 * 60 * MINUTE;
const now = Date.now();

export const recentSpaceSummaries: Array<{spaceId: string; pageCount: number; lastViewedAt: number}> = [
    {spaceId: 'project-avalanche', pageCount: 12, lastViewedAt: now - (12 * MINUTE)},
    {spaceId: 'incident-handbook', pageCount: 25, lastViewedAt: now - (18 * MINUTE)},
    {spaceId: 'contributor-wiki', pageCount: 48, lastViewedAt: now - (18 * MINUTE)},
    {spaceId: 'product-support', pageCount: 4, lastViewedAt: now - (2 * DAY)},
    {spaceId: 'release-discussion', pageCount: 5, lastViewedAt: now - (2 * DAY)},
    {spaceId: 'security-incident-handbook', pageCount: 5, lastViewedAt: now - (2 * DAY)},
];
