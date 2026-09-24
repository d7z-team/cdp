const path = require('path');
const TerserPlugin = require('terser-webpack-plugin');

module.exports = {
    mode: 'production',
    entry: {
        'main_runtime': './internal/webassets/src/cmd/main_runtime/index.ts',
        'core': './internal/webassets/src/cmd/core/index.ts',
    },
    module: {
        rules: [
            {
                test: /\.ts$/,
                use: 'ts-loader',
                exclude: /node_modules/
            }
        ]
    },
    resolve: {
        extensions: ['.ts', '.js']
    },
    optimization: {
        minimize: true,
        minimizer: [
            new TerserPlugin({
                terserOptions: {
                    compress: {
                        drop_console: true,
                        drop_debugger: true,
                    },
                },
            }),
        ],
    },
    output: {
        filename: '[name]/index.js',
        path: path.resolve(__dirname, 'internal', 'webassets', 'dest')
    },
    performance: {
        maxEntrypointSize: 1048576,
        maxAssetSize: 1048576
    }
};
