const exec = require('child_process').exec;

const path = require('path');

const webpack = require('webpack');

const PLUGIN_ID = require('../plugin.json').id;

const NPM_TARGET = process.env.npm_lifecycle_event; //eslint-disable-line no-process-env
const isDev = NPM_TARGET === 'debug' || NPM_TARGET === 'debug:watch';

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
                // Inline binary assets into the single plugin bundle, matching
                // Playbooks/Calls. Emitting separate resource files instead
                // (`asset`/`asset/resource`, plus code-split chunks) requires an
                // output.publicPath under the plugin's static route and a backend
                // handler to serve it (as Boards does) — which also becomes
                // necessary to support subpath-hosted Mattermost instances.
                // Adopt that when real, large binary assets or chunking land; no
                // such assets are imported today.
                test: /\.(png|eot|tiff|svg|woff2|woff|ttf|gif|mp3|jpg|jpeg)$/,
                type: 'asset/inline',
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
        publicPath: '/',
        filename: 'main.js',
    },
    mode: (isDev) ? 'eval-source-map' : 'production',
    plugins,
};

if (isDev) {
    Object.assign(config, {devtool: 'eval-source-map'});
}

module.exports = config;
