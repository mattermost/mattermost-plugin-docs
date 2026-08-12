// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createWriteStream, existsSync, mkdirSync, readFileSync, readdirSync, statSync, type WriteStream} from 'node:fs';
import {join, resolve} from 'node:path';

import {GenericContainer, Network, Wait, type StartedNetwork, type StartedTestContainer} from 'testcontainers';
import {PostgreSqlContainer, type StartedPostgreSqlContainer} from '@testcontainers/postgresql';

// Playwright transpiles to CommonJS; import.meta is unavailable at runtime.
const projectRoot = resolve(__dirname, '../..');
const repoRoot = resolve(projectRoot, '../..');

// Tracks server master rather than pinning a build. Docs depends on server features
// that only exist there — the EnableDocs feature flag and the Space channel type — and
// min_server_version rises as it evolves, so any pin eventually becomes too old rather
// than merely stale. assertSupportsDocs below turns a server that cannot run Docs into
// a named failure instead of opaque 501s from every plugin route.
//
// Pin via MM_IMAGE to reproduce a run or to bisect a server-side regression. Note the
// tag floats: it has been seen lagging behind server master by enough to miss a
// feature flag entirely, so a green run does not prove the newest server was used.
const defaultImage = 'mattermostdevelopment/mattermost-enterprise-edition:master';

const postgresImage = 'postgres:15';

export const adminUsername = 'sysadmin';
export const adminPassword = 'Sys@dmin-sample1';
const adminEmail = 'sysadmin@sample.mattermost.com';

export const defaultTeamName = 'ad-1';
const defaultTeamDisplayName = 'eligendi';

const pluginId = 'com.mattermost.docs';

// Mattermost's first boot runs schema migrations, and CI runners are slower than
// a dev laptop; testcontainers' 60s default is not enough headroom.
const startupTimeoutMs = 180_000;

function pluginBundlePath(): string {
    const distDir = join(repoRoot, 'dist');
    const bundles = existsSync(distDir) ? readdirSync(distDir).filter((f) => f.endsWith('.tar.gz')) : [];

    if (bundles.length === 0) {
        throw new Error(`No plugin bundle found in ${distDir}. Run "make dist" from the repo root first.`);
    }

    // BUNDLE_NAME embeds the git-derived PLUGIN_VERSION, so the name is not stable
    // enough to hard-code. Most recently written wins if a stale bundle is lying
    // around — sorting by name would order 0.10.0 before 0.9.0 and pick the older one.
    return bundles.
        map((name) => join(distDir, name)).
        sort((a, b) => statSync(b).mtimeMs - statSync(a).mtimeMs)[0];
}

function requiredServerVersion(): string {
    const manifest = JSON.parse(readFileSync(join(repoRoot, 'plugin.json'), 'utf8')) as {min_server_version: string};
    return manifest.min_server_version;
}

function isVersionAtLeast(actual: string, required: string): boolean {
    const toParts = (v: string) => v.split('.').map((n) => parseInt(n, 10) || 0);
    const [a, r] = [toParts(actual), toParts(required)];

    for (let i = 0; i < Math.max(a.length, r.length); i++) {
        const diff = (a[i] ?? 0) - (r[i] ?? 0);
        if (diff !== 0) {
            return diff > 0;
        }
    }

    return true;
}

export class DocsServerContainer {
    private container!: StartedTestContainer;
    private pgContainer!: StartedPostgreSqlContainer;
    private network!: StartedNetwork;
    private logStream?: WriteStream;

    url(): string {
        return `http://${this.container.getHost()}:${this.container.getMappedPort(8065)}`;
    }

    private async exec(command: string[]): Promise<string> {
        const {output, exitCode} = await this.container.exec(command);

        if (exitCode !== 0) {
            throw new Error(`Command failed (${exitCode}): ${command.join(' ')}\n${output}`);
        }

        return output;
    }

    async start(): Promise<DocsServerContainer> {
        const image = process.env.MM_IMAGE || defaultImage;

        this.network = await new Network().start();

        this.pgContainer = await new PostgreSqlContainer(postgresImage).
            withDatabase('mattermost_test').
            withUsername('mmuser').
            withPassword('mostest').
            withNetwork(this.network).
            withNetworkAliases('db').
            withStartupTimeout(startupTimeoutMs).
            start();

        this.container = await new GenericContainer(image).
            withEnvironment({
                MM_SQLSETTINGS_DRIVERNAME: 'postgres',
                MM_SQLSETTINGS_DATASOURCE: 'postgres://mmuser:mostest@db:5432/mattermost_test?sslmode=disable',

                // The Docs plugin returns 501 from every route unless this is on.
                MM_FEATUREFLAGS_ENABLEDOCS: 'true',

                // Local mode lets setup drive mmctl over a socket, so seeding the
                // first admin doesn't need an HTTP bootstrap.
                MM_SERVICESETTINGS_ENABLELOCALMODE: 'true',
                MM_PLUGINSETTINGS_ENABLEUPLOADS: 'true',

                // The plugin bundle is larger than the stock upload limit, which
                // otherwise rejects it at "plugin add".
                MM_FILESETTINGS_MAXFILESIZE: '256000000',

                // Extracting prepackaged plugins costs tens of seconds and can race
                // the plugin install below.
                MM_PLUGINSETTINGS_AUTOMATICPREPACKAGEDPLUGINS: 'false',
                MM_SERVICESETTINGS_EXPERIMENTALSTRICTCSRFENFORCEMENT: 'false',
                MM_SERVICESETTINGS_STRICTCSRFENFORCEMENT: 'false',
                MM_PASSWORDSETTINGS_MINIMUMLENGTH: '5',
                MM_SERVICESETTINGS_ENABLEONBOARDINGFLOW: 'false',

                // Otherwise every first navigation lands on the "open in the desktop
                // app?" interstitial instead of the product.
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

                // Truncate rather than append: a developer reading this after a failure
                // should see one run, not several interleaved.
                this.logStream = createWriteStream(join(projectRoot, 'logs', 'server-logs.log'), {flags: 'w'});
                stream.on('data', (data: string | Buffer) => this.logStream?.write(String(data)));
            }).
            start();

        // Anything failing from here on leaves two containers and a network running,
        // and globalSetup never gets to return its teardown closure. Ryuk would
        // normally reap them, but it is disabled in a number of CI images.
        try {
            await this.exec(['mmctl', '--local', 'config', 'set', 'ServiceSettings.SiteURL', this.url()]);
            await this.assertSupportsDocs();

            await this.createAdmin();
            await this.createTeam(defaultTeamName, defaultTeamDisplayName);
            await this.addUserToTeam(adminUsername, defaultTeamName);
            await this.installPlugin();
        } catch (error) {
            await this.stop();
            throw error;
        }

        return this;
    }

    // Fails loudly and specifically. Without this, an image lacking Docs core support
    // shows up much later as an unexplained 501 from every plugin route.
    private async assertSupportsDocs() {
        // Both checks read the server's own client config. `mmctl version` reports the
        // mmctl build rather than the server, so it only tracks min_server_version by
        // coincidence and would pass silently on an image where the two diverge.
        const response = await fetch(
            `${this.url()}/api/v4/config/client?format=old`,
            {signal: AbortSignal.timeout(30_000)},
        );
        const config = await response.json() as Record<string, string>;

        const required = requiredServerVersion();
        const version = config.Version;

        if (!version || !isVersionAtLeast(version, required)) {
            throw new Error(
                `Mattermost image reports version ${version ?? 'unknown'}, but the plugin requires >= ${required}. Pin a newer dev tag via MM_IMAGE.`,
            );
        }

        if (config.FeatureFlagEnableDocs !== 'true') {
            throw new Error(
                'Mattermost image does not support the EnableDocs feature flag, so it lacks Docs core support. Pin a newer dev tag via MM_IMAGE.',
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

    // "plugin enable" reports success even when activation then fails, leaving the
    // plugin listed as inactive — most often because the bundle carries no
    // linux-amd64 binary, which `make dist` omits when MM_SERVICESETTINGS_ENABLEDEVELOPER
    // is set in the shell. Without this check that surfaces much later, and far less
    // legibly, as the Docs UI simply never rendering.
    private async assertPluginRunning() {
        const output = await this.exec(['mmctl', '--local', 'plugin', 'list', '--json']);

        // mmctl brackets the JSON payload with human-readable lines of its own.
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

    async stop() {
        await this.container?.stop();
        await this.pgContainer?.stop();
        await this.network?.stop();

        await new Promise<void>((done) => {
            if (!this.logStream || this.logStream.writableEnded) {
                done();
                return;
            }
            this.logStream.end(done);
        });
    }
}
