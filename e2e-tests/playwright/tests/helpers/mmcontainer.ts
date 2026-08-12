// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createWriteStream, existsSync, mkdirSync, readFileSync, readdirSync, statSync, type WriteStream} from 'node:fs';
import {join, resolve} from 'node:path';

import {GenericContainer, Network, Wait, type StartedNetwork, type StartedTestContainer} from 'testcontainers';
import {PostgreSqlContainer, type StartedPostgreSqlContainer} from '@testcontainers/postgresql';

// Playwright transpiles to CommonJS, so import.meta is unavailable.
const projectRoot = resolve(__dirname, '../..');
const repoRoot = resolve(projectRoot, '../..');

// Local fallback; CI sets MM_IMAGE in ci.yml. Tracks master because stock releases lack
// Docs core support and min_server_version rises, so a pin goes stale.
const defaultImage = 'mattermostdevelopment/mattermost-enterprise-edition:master';

const postgresImage = 'postgres:15';

export const adminUsername = 'sysadmin';
export const adminPassword = 'Sys@dmin-sample1';
const adminEmail = 'sysadmin@sample.mattermost.com';

export const defaultTeamName = 'ad-1';
const defaultTeamDisplayName = 'eligendi';

const pluginId = 'com.mattermost.docs';

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

                // Every Docs route 501s without this.
                MM_FEATUREFLAGS_ENABLEDOCS: 'true',

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

        // Without this, a failure here leaks both containers and the network: globalSetup
        // never returns its teardown closure, and Ryuk is disabled on some CI images.
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

    private async assertSupportsDocs() {
        // Client config, not `mmctl version`: that reports the mmctl build, not the server.
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
