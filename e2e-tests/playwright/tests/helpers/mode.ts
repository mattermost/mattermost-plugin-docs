// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The space-permission specs need a server image carrying the RBAC work of the paired core branch
// (the companion mattermost core-repo branch this plugin is built against; CI selects its commit
// from the PR-body `e2e-core-commit` marker — see e2e-tests/playwright/README.md) and an
// Enterprise license, neither of which the default run has. They are therefore opt-in: the flag
// selects the specs (playwright.config.ts) and the container setup they require (mmcontainer.ts),
// which have to agree — a run that collects those specs without the licensed setup fails in every
// one of them, and a run that carries the licensed setup without collecting the specs tests nothing.
export const spacePermissionsMode = process.env.MM_E2E_SPACE_PERMISSIONS === 'true';
