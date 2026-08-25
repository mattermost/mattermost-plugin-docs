// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createWriteStream, existsSync, mkdirSync, readFileSync, readdirSync, statSync, type WriteStream} from 'node:fs';
import {join, resolve} from 'node:path';

import {GenericContainer, Network, Wait, type StartedNetwork, type StartedTestContainer} from 'testcontainers';
import {PostgreSqlContainer, type StartedPostgreSqlContainer} from '@testcontainers/postgresql';

import {spacePermissionsMode} from './mode';
import {assertServerSupportsDocs, pluginId} from './preflight';

// Playwright transpiles to CommonJS, so import.meta is unavailable.
const projectRoot = resolve(__dirname, '../..');
const repoRoot = resolve(projectRoot, '../..');

// Core CI publishes one image per commit of the paired branch, tagged with the commit's first
// seven characters.
const coreImageRepo = 'mattermostdevelopment/mattermost-team-edition';
const corePinFile = join(repoRoot, 'build/core-commit.txt');

// Every spec needs a server carrying the paired core branch's work, not only the permission ones:
// CreateSpace resolves a preset space scheme that core seeds there, so on :master even the
// authoring specs fail at the first space. The image is therefore derived from the same pin the Go
// suite verifies CORE_IMAGE against, which keeps one commit naming the server both suites run
// against. Once those core changes ship in a release, this can go back to a release tag.
function resolveImage(): string {
    const image = process.env.MM_IMAGE;

    if (image) {
        return image;
    }

    const pinned = readFileSync(corePinFile, 'utf8').
        split('\n').
        map((line) => line.trim()).
        find((line) => line !== '' && !line.startsWith('#'));

    if (!pinned || !(/^[0-9a-f]{7,40}$/).test(pinned)) {
        throw new Error(`${corePinFile} must hold one core commit sha; got '${pinned ?? ''}'.`);
    }

    return `${coreImageRepo}:${pinned.slice(0, 7)}`;
}

// The images core CI publishes are built for amd64 only, so on an arm64 host the pull fails with
// "no matching manifest for linux/arm64/v8" before a single spec runs. testcontainers does not read
// DOCKER_DEFAULT_PLATFORM, so the platform has to be passed to the container explicitly; the env
// var is honored here so the setting a developer already exports for `docker` still applies.
// Emulated, the server boots slowly — the migration wait below has room for it.
//
// Only the Mattermost image needs this. The Postgres image publishes an arm64 manifest, and
// pinning it to amd64 would make it emulate for nothing.
//
// A locally built image is the exception, and pinning it is worse than not pinning:
// build/build-core-image.sh targets the Docker daemon's own architecture, so on an arm64 host it
// produces an arm64 image, and asking for amd64 fails the boot outright with "does not provide the
// specified platform" — there is no manifest to fall back on and nothing to pull. A bare tag means
// locally built here for the same reason it does in the Go suite: a published image is namespaced.
function resolvePlatform(image: string): string | undefined {
    const explicit = (process.env.MM_E2E_PLATFORM ?? process.env.DOCKER_DEFAULT_PLATFORM ?? '').trim();

    if (explicit) {
        return explicit;
    }

    if (!image.includes('/')) {
        return undefined;
    }

    return process.arch === 'arm64' ? 'linux/amd64' : undefined;
}

// resolveLicense supplies the Enterprise license the guest scenarios need: demoting a user to a
// guest requires GuestAccountsSettings.Enable, which an unlicensed server refuses (see
// helpers/guest.ts). Under spacePermissionsMode absence is therefore a hard error naming both
// sources rather than a skip, so a run never quietly narrows what it proves. The authoring specs
// touch no licensed feature and run unlicensed.
//
// The license is never committed: MM_LICENSE carries it directly (how CI supplies it from a
// secret), MM_LICENSE_FILE names a file holding it (how a developer points at a local copy).
function resolveLicense(): string | undefined {
    const direct = (process.env.MM_LICENSE ?? '').trim();

    if (direct) {
        return direct;
    }

    const file = (process.env.MM_LICENSE_FILE ?? '').trim();

    if (file) {
        const contents = readFileSync(file, 'utf8').trim();

        if (!contents) {
            throw new Error(`MM_LICENSE_FILE points at ${file}, which is empty.`);
        }

        return contents;
    }

    if (spacePermissionsMode) {
        throw new Error(
            'No Enterprise license found. The space-permission scenarios demote a user to a guest, which ' +
            'requires GuestAccountsSettings.Enable — a licensed feature. Set MM_LICENSE to the raw license, ' +
            'or MM_LICENSE_FILE to a path holding it.',
        );
    }

    return undefined;
}

const postgresImage = 'postgres:15';

// A role the paired core branch seeds as a default. Probed after boot to tell a server that carries
// the space-permission work from one that merely has the EnableDocs flag.
const spacePermissionProbeRole = 'docs_pg_create';

// Matches the Go suite's own budget for the same wait. Generous because the migration job
// competes with first-boot schema work, and more so on an emulated architecture.
const phase2MigrationTimeoutMs = 120_000;

export const adminUsername = 'sysadmin';
export const adminPassword = 'Sys@dmin-sample1';
const adminEmail = 'sysadmin@sample.mattermost.com';

export const defaultTeamName = 'ad-1';
const defaultTeamDisplayName = 'eligendi';

// First boot runs schema migrations; testcontainers' 60s default is too tight.
const startupTimeoutMs = 180_000;

function pluginBundlePath(): string {
    const distDir = join(repoRoot, 'dist');
    const bundles = existsSync(distDir) ? readdirSync(distDir).filter((f) => f.endsWith('.tar.gz')) : [];

    if (bundles.length === 0) {
        throw new Error(`No plugin bundle found in ${distDir}. Run "make dist" from the repo root first.`);
    }

    // By mtime, not name: 0.10.0 would sort before 0.9.0.
    return bundles.
        map((name) => join(distDir, name)).
        sort((a, b) => statSync(b).mtimeMs - statSync(a).mtimeMs)[0];
}

export class DocsServerContainer {
    private container!: StartedTestContainer;
    private pgContainer!: StartedPostgreSqlContainer;
    private network!: StartedNetwork;
    private logStream?: WriteStream;

    url(): string {
        return `http://${this.container.getHost()}:${this.container.getMappedPort(8065)}`;
    }

    // displayCommand names the command in a failure without reproducing its arguments.
    // Defaults to the command itself; a caller passing a secret overrides it, since this
    // message reaches the terminal and the CI job log.
    private async exec(command: string[], displayCommand = command.join(' ')): Promise<string> {
        const {output, exitCode} = await this.container.exec(command);

        if (exitCode !== 0) {
            throw new Error(`Command failed (${exitCode}): ${displayCommand}\n${output}`);
        }

        return output;
    }

    // Every allocation lives inside one try: a failure past the first one leaks containers
    // and the network, because globalSetup never returns its teardown closure and Ryuk is
    // disabled on some CI images.
    async start(): Promise<DocsServerContainer> {
        const image = resolveImage();

        // Resolved here rather than at its point of use: it is a pure environment check with the
        // same failure semantics as resolveImage, and leaving it below meant a CI job with an unset
        // license secret paid a full image pull and boot to learn it.
        const license = resolveLicense();

        try {
            await this.startContainers(image);

            await this.exec(['mmctl', '--local', 'config', 'set', 'ServiceSettings.SiteURL', this.url()]);
            await assertServerSupportsDocs(this.url(), 'Pin a newer dev tag via MM_IMAGE.');

            if (license) {
                await this.applyLicense(license);
            }

            await this.createAdmin();

            // After createAdmin: both need a session. Both run for every spec, not only the
            // permission ones — core reads a scheme through the same phase-2 gate whichever route
            // asks, and CreateSpace resolves a preset scheme, so an authoring run that skipped
            // these would fail at its first space with the migration error or a nil scheme.
            await this.waitForPhase2Migration();
            await this.assertSupportsSpacePermissions();

            await this.createTeam(defaultTeamName, defaultTeamDisplayName);
            await this.addUserToTeam(adminUsername, defaultTeamName);
            await this.installPlugin();
        } catch (error) {
            await this.stop();
            throw error;
        }

        return this;
    }

    private async startContainers(image: string) {
        this.network = await new Network().start();

        this.pgContainer = await new PostgreSqlContainer(postgresImage).
            withDatabase('mattermost_test').
            withUsername('mmuser').
            withPassword('mostest').
            withNetwork(this.network).
            withNetworkAliases('db').
            withStartupTimeout(startupTimeoutMs).
            start();

        // Assigned before the chain rather than inside it: withPlatform must not be called at all
        // when no platform applies, and the fluent chain has no way to skip a link.
        const platform = resolvePlatform(image);
        let server = new GenericContainer(image);

        if (platform) {
            server = server.withPlatform(platform);
        }

        this.container = await server.
            withEnvironment({
                MM_SQLSETTINGS_DRIVERNAME: 'postgres',
                MM_SQLSETTINGS_DATASOURCE: 'postgres://mmuser:mostest@db:5432/mattermost_test?sslmode=disable',

                // Every Docs route 501s without this.
                MM_FEATUREFLAGS_ENABLEDOCS: 'true',

                // The published images are production builds, which default to the production
                // service environment and reject a test/development license outright. The
                // licensed permission scenarios therefore need this set.
                MM_SERVICEENVIRONMENT: 'test',

                // Lets setup drive mmctl over a socket instead of bootstrapping over HTTP.
                MM_SERVICESETTINGS_ENABLELOCALMODE: 'true',
                MM_PLUGINSETTINGS_ENABLEUPLOADS: 'true',

                // The bundle exceeds the stock upload limit.
                MM_FILESETTINGS_MAXFILESIZE: '256000000',

                // Extraction is slow and races the plugin install below.
                MM_PLUGINSETTINGS_AUTOMATICPREPACKAGEDPLUGINS: 'false',
                MM_SERVICESETTINGS_EXPERIMENTALSTRICTCSRFENFORCEMENT: 'false',
                MM_SERVICESETTINGS_STRICTCSRFENFORCEMENT: 'false',
                MM_PASSWORDSETTINGS_MINIMUMLENGTH: '5',
                MM_SERVICESETTINGS_ENABLEONBOARDINGFLOW: 'false',

                // Otherwise navigation lands on the desktop-app interstitial.
                MM_SERVICESETTINGS_ENABLEDESKTOPLANDINGPAGE: 'false',
                MM_LOGSETTINGS_CONSOLELEVEL: 'DEBUG',
            }).
            withExposedPorts(8065).
            withNetwork(this.network).
            withNetworkAliases('mattermost').
            withWaitStrategy(Wait.forLogMessage('Server is listening on')).
            withStartupTimeout(startupTimeoutMs).
            withLogConsumer((stream) => {
                mkdirSync(join(projectRoot, 'logs'), {recursive: true});

                // Truncate: reading interleaved runs after a failure is worse than one.
                this.logStream = createWriteStream(join(projectRoot, 'logs', 'server-logs.log'), {flags: 'w'});
                stream.on('data', (data: string | Buffer) => this.logStream?.write(String(data)));
            }).
            start();
    }

    private async applyLicense(license: string) {
        // upload-string rather than upload: the license arrives as an environment value, so there is
        // no file to copy into the container. The command is named without its argument, so a
        // rejected license does not print itself into the log.
        await this.exec(
            ['mmctl', '--local', 'license', 'upload-string', license],
            'mmctl --local license upload-string <license>',
        );
    }

    private async adminToken(): Promise<string> {
        const login = await fetch(`${this.url()}/api/v4/users/login`, {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({login_id: adminUsername, password: adminPassword}),
            signal: AbortSignal.timeout(30_000),
        });
        const token = login.headers.get('token');

        if (!login.ok || !token) {
            throw new Error(`Could not log in as ${adminUsername} (HTTP ${login.status}).`);
        }

        return token;
    }

    // The advanced-permissions phase-2 migration runs as a job after boot, and every
    // scheme-backed route answers 501 until it finishes — so the first CreateSpace fails
    // with app.space.create.admin_role_failed wrapping
    // app.schemes.is_phase_2_migration_completed.not_completed.
    private async waitForPhase2Migration() {
        const token = await this.adminToken();
        const deadline = Date.now() + phase2MigrationTimeoutMs;
        let lastBody = '';

        while (Date.now() < deadline) {
            const response = await fetch(`${this.url()}/api/v4/schemes?page=0&per_page=1`, {
                headers: {Authorization: `Bearer ${token}`},
                signal: AbortSignal.timeout(30_000),
            });

            if (response.ok) {
                return;
            }

            lastBody = await response.text();

            // Only a pending migration is worth waiting out. A bad token or a 500 will not
            // resolve on its own, so it surfaces now rather than after two minutes that would
            // report it as a migration which never finished.
            if (!lastBody.includes('is_phase_2_migration_completed')) {
                throw new Error(
                    `Polling for the advanced-permissions phase-2 migration failed (HTTP ${response.status}): ${lastBody}`,
                );
            }

            await new Promise((resolve) => setTimeout(resolve, 2_000));
        }

        throw new Error(
            `The advanced-permissions phase-2 migration did not complete within ${phase2MigrationTimeoutMs}ms. Last response: ${lastBody}`,
        );
    }

    // An image that predates the paired core branch boots cleanly, reports EnableDocs on, and then
    // fails permission assertions as unexplained 403s. Probing a role that branch seeds as a default
    // turns that into one named failure at setup.
    private async assertSupportsSpacePermissions() {
        const token = await this.adminToken();

        const response = await fetch(`${this.url()}/api/v4/roles/name/${spacePermissionProbeRole}`, {
            headers: {Authorization: `Bearer ${token}`},
            signal: AbortSignal.timeout(30_000),
        });

        if (!response.ok) {
            throw new Error(
                `Mattermost image does not define the ${spacePermissionProbeRole} role (HTTP ${response.status}), ` +
                'so it predates the paired core branch\'s space-permission work. Point MM_IMAGE at the image core ' +
                'CI published for the commit named in build/core-commit.txt.',
            );
        }
    }

    private async createAdmin() {
        await this.exec([
            'mmctl', '--local', 'user', 'create',
            '--email', adminEmail,
            '--username', adminUsername,
            '--password', adminPassword,
            '--system-admin',
            '--email-verified',
        ]);
    }

    async createTeam(name: string, displayName: string) {
        await this.exec(['mmctl', '--local', 'team', 'create', '--name', name, '--display-name', displayName]);
    }

    async addUserToTeam(username: string, teamName: string) {
        await this.exec(['mmctl', '--local', 'team', 'users', 'add', teamName, username]);
    }

    private async installPlugin() {
        const bundle = pluginBundlePath();

        await this.container.copyFilesToContainer([{source: bundle, target: '/tmp/plugin.tar.gz'}]);
        await this.exec(['mmctl', '--local', 'plugin', 'add', '/tmp/plugin.tar.gz']);
        await this.exec(['mmctl', '--local', 'plugin', 'enable', pluginId]);

        await this.assertPluginRunning();
    }

    // "plugin enable" reports success even when activation then fails, which otherwise
    // surfaces only as the Docs UI never rendering.
    private async assertPluginRunning() {
        const output = await this.exec(['mmctl', '--local', 'plugin', 'list', '--json']);

        const payload = output.slice(output.indexOf('['), output.lastIndexOf(']') + 1);
        const [{active}] = JSON.parse(payload) as Array<{active: Array<{id: string}>}>;

        if (!active.some((plugin) => plugin.id === pluginId)) {
            throw new Error(
                `Plugin ${pluginId} installed but did not activate. See logs/server-logs.log. ` +
                'If activation failed on a missing linux-amd64 binary, rebuild via "make test-e2e" (or ' +
                'unset MM_SERVICESETTINGS_ENABLEDEVELOPER before "make dist") so the bundle includes it.',
            );
        }
    }

    // Containers before the network, and each failure is contained: a stop() that threw
    // would strand the remaining resources and replace the setup error that called it.
    async stop() {
        await this.stopQuietly('Mattermost container', () => this.container?.stop());
        await this.stopQuietly('Postgres container', () => this.pgContainer?.stop());
        await this.stopQuietly('Docker network', () => this.network?.stop());

        await new Promise<void>((done) => {
            if (!this.logStream || this.logStream.writableEnded) {
                done();
                return;
            }
            this.logStream.end(done);
        });
    }

    private async stopQuietly(what: string, stop: () => Promise<unknown> | undefined) {
        try {
            await stop();
        } catch (error) {
            console.error(`[e2e] Failed to stop the ${what}:`, error);
        }
    }
}
