import * as path from 'path';
import { ConsoleRemotePlugin } from '@openshift-console/dynamic-plugin-sdk-webpack';
import CopyWebpackPlugin from 'copy-webpack-plugin';
import { Configuration } from 'webpack';
import { Configuration as DevServerConfiguration } from 'webpack-dev-server';

const isProd = process.env.NODE_ENV === 'production';

const DEV_PORT = 9001;
const DEV_DEFAULT_ALLOWED_ORIGIN = 'http://localhost:3000';
const DEV_ALLOWED_ORIGIN_VAR = 'PLUGIN_DEV_ALLOWED_ORIGIN';

// The console loads the plugin cross-origin from the dev server, which also
// hands out source maps, so its CORS and Host policy must name the console
// rather than admit every origin: a wildcard, or allowedHosts 'all' (which
// webpack documents as open to DNS rebinding), lets any page the developer
// visits read the plugin sources. One validated knob drives both.
// PLUGIN_DEV_ALLOWED_ORIGIN is the console's own origin; a LAN console or an
// SSH-forwarded one sets it, and its hostname joins the Host allowlist.
function devAllowedOrigin(): URL {
  const raw =
    (process.env[DEV_ALLOWED_ORIGIN_VAR] ?? '').trim() || DEV_DEFAULT_ALLOWED_ORIGIN;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new Error(
      `${DEV_ALLOWED_ORIGIN_VAR} must be an absolute origin such as ${DEV_DEFAULT_ALLOWED_ORIGIN}, got ${JSON.stringify(raw)}`,
    );
  }
  if (
    (url.protocol !== 'http:' && url.protocol !== 'https:') ||
    url.username ||
    url.password ||
    url.pathname !== '/' ||
    url.search ||
    url.hash
  ) {
    throw new Error(
      `${DEV_ALLOWED_ORIGIN_VAR} must be a bare http(s) origin (scheme, host, optional port), got ${JSON.stringify(raw)}`,
    );
  }
  return url;
}

// devServer is ignored by a production build, so the dev-only origin is read
// only here: a mis-set PLUGIN_DEV_ALLOWED_ORIGIN must never fail `yarn build`.
function devServerOptions(): DevServerConfiguration {
  const origin = devAllowedOrigin();
  const hosts = ['localhost'];
  if (origin.hostname !== 'localhost' && !hosts.includes(origin.hostname)) {
    hosts.push(origin.hostname);
  }
  return {
    static: path.resolve(__dirname, 'dist'),
    port: DEV_PORT,
    devMiddleware: { writeToDisk: true },
    allowedHosts: hosts,
    headers: {
      'Access-Control-Allow-Origin': origin.origin,
      'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, PATCH, OPTIONS',
      'Access-Control-Allow-Headers': 'X-Requested-With, Content-Type, Authorization',
    },
  };
}

const config: Configuration & { devServer?: DevServerConfiguration } = {
  mode: isProd ? 'production' : 'development',
  context: path.resolve(__dirname, 'src'),
  // Empty: ConsoleRemotePlugin below injects one entry per console-extensions.json
  // $codeRef, which resolves to a default export by name. The two today are
  // CompliancePage (console.page/route) and ClusterScoreItem (dashboard item).
  entry: {},
  // Production: no persistent webpack cache (avoids host-local cache keys in outputs).
  // Dev keeps an in-memory cache for rebuild speed.
  cache: isProd ? false : { type: 'memory' },
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
  // No source maps in production artifacts (would embed paths / leak sources).
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
  devServer: devServerOptions(),
};

export default config;
