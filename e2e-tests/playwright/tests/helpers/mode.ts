// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The space-permission specs need a server image carrying the paired core branch's RBAC work and
// an Enterprise license, neither of which the default run has. They are therefore opt-in: the flag
// selects the specs (playwright.config.ts) and the container setup they require (mmcontainer.ts),
// which have to agree — a run that collects those specs without the licensed setup fails in every
// one of them, and a run that pays for the setup without them proves nothing.
export const spacePermissionsMode = process.env.MM_E2E_SPACE_PERMISSIONS === 'true';
