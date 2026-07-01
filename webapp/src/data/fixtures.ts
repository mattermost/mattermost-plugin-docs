// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Page, Space} from 'types/docs';

// Design-time fixtures behind the mock data source. The real Docs API replaces
// these once the server contract exists; only the mock source imports them.

export const spaces: Space[] = [
    {id: 'project-avalanche', name: 'Project Avalanche', emoji: '✈️'},
    {id: 'contributor-wiki', name: 'Contributor Wiki', emoji: '👨🏻‍💻'},
    {id: 'developers', name: 'Developers', emoji: '⌨️'},
    {id: 'release-discussion', name: 'Release Discussion', emoji: '🚀'},
    {id: 'incident-handbook', name: 'Incident Handbook', emoji: '📕'},
    {id: 'security-incident-handbook', name: 'Security Incident Handbook', emoji: '🛡️'},
    {id: 'product-support', name: 'Product Support', emoji: '🖐️'},
];

export const pages: Page[] = [
    {id: 'operational-procedures', title: 'Operational Procedures', spaceId: 'project-avalanche', spaceName: 'Project Avalanche'},
    {id: 'overview-flight-comms', title: 'Overview of Flight Communication Protocols', spaceId: 'project-avalanche', spaceName: 'Project Avalanche'},
    {id: 'communication-protocols', title: 'Communication Protocols', spaceId: 'project-avalanche', spaceName: 'Project Avalanche'},
];

export const recentSpaceIds = ['project-avalanche'];
export const recentPageIds = ['operational-procedures', 'overview-flight-comms', 'communication-protocols'];

// Recently-viewed-spaces summaries for the Home listing (page counts and
// viewed labels are mocked until the server provides them).
export const recentSpaceSummaries: Array<{spaceId: string; pageCount: number; viewedLabel: string}> = [
    {spaceId: 'project-avalanche', pageCount: 12, viewedLabel: 'Viewed 12m ago'},
    {spaceId: 'incident-handbook', pageCount: 25, viewedLabel: 'Viewed 18m ago'},
    {spaceId: 'contributor-wiki', pageCount: 48, viewedLabel: 'Viewed 18m ago'},
    {spaceId: 'product-support', pageCount: 4, viewedLabel: 'Viewed 2d ago'},
    {spaceId: 'release-discussion', pageCount: 5, viewedLabel: 'Viewed 2d ago'},
    {spaceId: 'security-incident-handbook', pageCount: 5, viewedLabel: 'Viewed 2d ago'},
];
