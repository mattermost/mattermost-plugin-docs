const exec = require('child_process').exec;

const crypto = require('crypto');

const fs = require('fs');

const path = require('path');

const webpack = require('webpack');

const PLUGIN_ID = require('../plugin.json').id;

const NPM_TARGET = process.env.npm_lifecycle_event; //eslint-disable-line no-process-env
const isDev = NPM_TARGET === 'debug' || NPM_TARGET === 'debug:watch';
const isAnalyze = NPM_TARGET === 'build:analyze';

// Excluded rather than allowlisted, so a new source directory cannot silently leave the token stale. Dotfiles and junit.xml are build-irrelevant artifacts; dist is webpack's own output.
const DIGEST_EXCLUDED = new Set(['node_modules', 'dist', 'junit.xml']);

function digestPath(digest, absolute, relative) {
    const stats = fs.lstatSync(absolute);
    if (stats.isDirectory()) {
        for (const entry of fs.readdirSync(absolute).sort()) {
            if (entry.startsWith('.') || DIGEST_EXCLUDED.has(entry)) {
                continue;
            }

            digestPath(digest, path.join(absolute, entry), `${relative}/${entry}`);
        }
        return;
    }

    const content = stats.isSymbolicLink() ? Buffer.from(fs.readlinkSync(absolute)) : fs.readFileSync(absolute);
    digest.update(`${relative}\0${content.length}\0`);
    digest.update(content);
}

// Identifies the build so an upgraded bundle never shares a chunk-loading global with the previous one still live in the page.
function resolveBuildToken() {
    const digest = crypto.createHash('sha256');

    // Debug and production emit very different bundles from identical sources.
    digest.update(isDev ? 'development' : 'production');
    digestPath(digest, __dirname, '.');

    return digest.digest('hex').slice(0, 16);
}

const BUILD_TOKEN = resolveBuildToken();

const plugins = [
    // Transitive deps read a bare `process` at runtime; the debug build keeps those paths and throws without this.
    new webpack.ProvidePlugin({
        process: 'process/browser.js',
    }),
];
if (NPM_TARGET === 'build:watch' || NPM_TARGET === 'debug:watch') {
    plugins.push({
        apply: (compiler) => {
            compiler.hooks.watchRun.tap('WatchStartPlugin', () => {
                // eslint-disable-next-line no-console
                console.log('Change detected. Rebuilding webapp.');
            });
            compiler.hooks.afterEmit.tap('AfterEmitPlugin', () => {
                exec('cd .. && make deploy-from-watch', (err, stdout, stderr) => {
                    if (stdout) {
                        process.stdout.write(stdout);
                    }
                    if (stderr) {
                        process.stderr.write(stderr);
                    }
                });
            });
        },
    });
}

if (isAnalyze) {
    // eslint-disable-next-line global-require
    const {BundleAnalyzerPlugin} = require('webpack-bundle-analyzer');
    plugins.push(new BundleAnalyzerPlugin({
        analyzerMode: 'static',
        reportFilename: 'bundle-report.html',
        openAnalyzer: true,
    }));
}

const config = {
    entry: [
        './src/index.tsx',
    ],
    resolve: {
        modules: [
            'src',
            'node_modules',
        ],
        extensions: ['*', '.js', '.jsx', '.ts', '.tsx'],
    },
    module: {
        rules: [
            {
                test: /\.(js|jsx|ts|tsx)$/,
                exclude: /node_modules/,
                use: {
                    loader: 'babel-loader',
                    options: {
                        cacheDirectory: true,

                        // Babel configuration is in babel.config.js because jest requires it to be there.
                    },
                },
            },
            {
                test: /\.(scss|css)$/,
                use: [
                    'style-loader',
                    {
                        loader: 'css-loader',
                        options: {
                            // Scopes *.module.(s)css classes so plugin styles cannot collide with the host web app's.
                            modules: {
                                auto: true,
                                localIdentName: isDev ? 'docs-[name]__[local]' : 'docs-[hash:base64:6]',
                            },
                        },
                    },
                    {
                        loader: 'sass-loader',
                        options: {
                            sassOptions: {
                                includePaths: ['node_modules/compass-mixins/lib', 'sass'],
                            },
                        },
                    },
                ],
            },
            {
                test: /\.(png|eot|tiff|svg|woff2|woff|ttf|gif|mp3|jpg|jpeg)$/,
                type: 'asset/resource',
                generator: {
                    filename: 'assets/[name].[contenthash][ext]',
                },
            },
        ],
    },
    externals: {
        react: 'React',
        'react-dom': 'ReactDOM',
        redux: 'Redux',
        'react-redux': 'ReactRedux',
        'prop-types': 'PropTypes',
        'react-bootstrap': 'ReactBootstrap',
        'react-router-dom': 'ReactRouterDom',
        'react-intl': 'ReactIntl',
    },
    output: {
        devtoolNamespace: PLUGIN_ID,
        uniqueName: `${PLUGIN_ID}_${BUILD_TOKEN}`,
        path: path.join(__dirname, '/dist'),

        // The server copies all of dist on every deploy, so stale chunks would accumulate forever.
        clean: true,

        // Resolves chunk and asset URLs from the served plugin path, keeping subpath-hosted instances working.
        publicPath: 'auto',
        filename: 'main.js',
        chunkFilename: '[name].[contenthash].js',
    },

    mode: isDev ? 'development' : 'production',
    plugins,
};

if (isDev) {
    Object.assign(config, {devtool: 'eval-source-map'});
}

module.exports = config;
