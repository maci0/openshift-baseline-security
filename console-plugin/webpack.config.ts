import * as path from 'path';
import { ConsoleRemotePlugin } from '@openshift-console/dynamic-plugin-sdk-webpack';
import CopyWebpackPlugin from 'copy-webpack-plugin';
import { Configuration } from 'webpack';
import { Configuration as DevServerConfiguration } from 'webpack-dev-server';

const isProd = process.env.NODE_ENV === 'production';

const config: Configuration & { devServer?: DevServerConfiguration } = {
  mode: isProd ? 'production' : 'development',
  context: path.resolve(__dirname, 'src'),
  entry: {},
  output: {
    path: path.resolve(__dirname, 'dist'),
    // contenthash: stable across rebuilds when file contents are unchanged (unlike [hash]).
    filename: isProd ? '[name]-bundle-[contenthash].min.js' : '[name]-bundle.js',
    chunkFilename: isProd ? '[name]-chunk-[contenthash].min.js' : '[name]-chunk.js',
    // Explicit deterministic hash (webpack 5 default); keeps chunk names stable across machines.
    hashFunction: 'xxhash64',
    // Do not embed module path comments in the bundle (absolute paths differ by host).
    pathinfo: false,
    clean: true,
  },
  resolve: {
    extensions: ['.ts', '.tsx', '.js', '.jsx'],
  },
  module: {
    rules: [
      {
        test: /\.(jsx?|tsx?)$/,
        exclude: /node_modules/,
        use: [{ loader: 'ts-loader', options: { transpileOnly: true } }],
      },
    ],
  },
  devtool: isProd ? false : 'source-map',
  optimization: {
    // Deterministic ids keep chunk graphs stable across machines for the same inputs.
    moduleIds: isProd ? 'deterministic' : 'named',
    chunkIds: isProd ? 'deterministic' : 'named',
    // Hash from post-minimize content so identical inputs yield identical filenames.
    realContentHash: isProd,
    minimize: isProd,
  },
  plugins: [
    new ConsoleRemotePlugin(),
    new CopyWebpackPlugin({
      patterns: [{ from: path.resolve(__dirname, 'locales'), to: 'locales' }],
    }),
  ],
  devServer: {
    static: path.resolve(__dirname, 'dist'),
    port: 9001,
    devMiddleware: { writeToDisk: true },
    allowedHosts: 'all',
    headers: {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, PATCH, OPTIONS',
      'Access-Control-Allow-Headers': 'X-Requested-With, Content-Type, Authorization',
    },
  },
};

export default config;
