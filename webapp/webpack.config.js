const exec = require('child_process').exec;

const path = require('path');

const webpack = require('webpack');

const PLUGIN_ID = require('../plugin.json').id;

const NPM_TARGET = process.env.npm_lifecycle_event; //eslint-disable-line no-process-env
const isDev = NPM_TARGET === 'debug' || NPM_TARGET === 'debug:watch';
const isAnalyze = NPM_TARGET === 'build:analyze';

const plugins = [
    // Shim Node's `process` for transitive deps whose Node-detection paths read
    // a bare `process` (e.g. `process && process.versions`) at runtime. The
    // production build strips these under minification, but the debug build
    // keeps them and throws "process is not defined" without this shim.
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
    // Only loaded for `npm run build:analyze`, so it stays out of normal builds.
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
                            // CSS Modules for *.module.(s)css only (auto-detected by
                            // filename). Scopes every class to a hashed name so the
                            // plugin's styles can never collide with or leak into the
                            // host web app (e.g. core's own .GenericModal). Readable
                            // names in dev; opaque hashes in production.
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
                // Emit binary assets as separate files (not inlined) so large
                // assets don't bloat the bundle. With output.publicPath: 'auto'
                // the runtime resolves them relative to the served plugin bundle,
                // so the host serves them from the plugin's static path without a
                // hardcoded URL — the same mechanism the async chunks rely on.
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
        path: path.join(__dirname, '/dist'),

        // Content-hashed backstage chunks are replaced on every build. Without cleaning, every
        // historical permission UI remains in dist and is packed into the plugin tarball, making
        // local/CI artifacts larger and leaving stale implementations available to be served.
        clean: true,

        // 'auto' makes webpack resolve async-chunk and asset/resource URLs at
        // runtime from where the plugin bundle is served (its static path),
        // rather than the site root '/'. This lets code-split chunks and emitted
        // assets load without a hardcoded plugin URL and keeps subpath-hosted
        // Mattermost instances working.
        publicPath: 'auto',
        filename: 'main.js',
        chunkFilename: '[name].[contenthash].js',
    },

    mode: (isDev) ? 'eval-source-map' : 'production',
    plugins,
};

if (isDev) {
    Object.assign(config, {devtool: 'eval-source-map'});
}

module.exports = config;
