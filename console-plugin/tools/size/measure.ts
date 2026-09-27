// Measure what the browser downloads from dist/.
//
// Every file webpack or `yarn licenses` leaves in dist/ is served by nginx out
// of the document root, except the license notice, which nothing links: it is
// counted out of the totals below, and the served non-JS files (manifest,
// locales) are added to the initial JS to report what a first paint waits on.
//
// The class of a JS file comes from the two webpack output name templates in
// webpack.config.ts: entry bundles are `[name]-bundle-[contenthash].min.js` and
// async chunks are `[name]-chunk-[contenthash].min.js`. A `.js` file matching
// neither is a build-config change the budget does not understand, so it is
// reported as unclassified and fails the gate rather than slipping past the
// initial-JS ceiling.
import type { SizeBudget } from './budget';

export type AssetClass = 'initial-js' | 'async-js';

export interface DistFile {
	readonly path: string;
	readonly rawBytes: number;
	readonly gzipBytes: number;
}

export interface AssetSize extends DistFile {
	readonly class: AssetClass;
}

export interface SizeReport {
	readonly assets: readonly AssetSize[];
	readonly initialJsGzipBytes: number;
	readonly asyncJsGzipBytes: number;
	// What a cold cache downloads before the first frame paints: the entry
	// bundles plus what the console fetches ahead of them, the plugin manifest
	// and the locale bundle. A growing locale file is a first-paint regression
	// the JS-only figure above cannot see.
	readonly criticalPathGzipBytes: number;
	readonly distGzipBytes: number;
	readonly unclassifiedJs: readonly string[];
}

export interface BudgetBreach {
	readonly label: string;
	readonly actualBytes: number;
	readonly budgetBytes: number;
}

const ENTRY_MARKER = '-bundle-';
const CHUNK_MARKER = '-chunk-';
const JS_SUFFIX = '.js';
// The one file in dist/ no browser ever requests. `yarn licenses` writes it
// next to the bundles, and the Dockerfile serves the same bytes from
// /licenses/; nothing in the page, the manifest, or the console's plugin loader
// links it. Counting it would let the tree grow on license text alone and push
// the real ceilings out of reach.
const UNSERVED_FILE = 'THIRD-PARTY-NOTICES.txt';

export const isJs = (relativePath: string): boolean => relativePath.endsWith(JS_SUFFIX);

// Served by nginx out of the document root (every other file webpack or the
// license step leaves in dist/).
export const isServedToBrowser = (relativePath: string): boolean => relativePath !== UNSERVED_FILE;

export function classifyAsset(relativePath: string): AssetClass | undefined {
	if (!relativePath.endsWith(JS_SUFFIX)) {
		return undefined;
	}
	const base = relativePath.slice(relativePath.lastIndexOf('/') + 1);
	if (base.includes(ENTRY_MARKER)) {
		return 'initial-js';
	}
	if (base.includes(CHUNK_MARKER)) {
		return 'async-js';
	}
	return undefined;
}

function sumGzip(assets: readonly AssetSize[], matches: (asset: AssetSize) => boolean): number {
	return assets.filter(matches).reduce((total, asset) => total + asset.gzipBytes, 0);
}

export function distGzip(files: readonly DistFile[]): number {
	return files.reduce(
		(total, file) => (isServedToBrowser(file.path) ? total + file.gzipBytes : total),
		0,
	);
}

// Served non-JS: the manifest and the locale bundles the console fetches
// before it executes an entry bundle.
export function servedNonJsGzip(files: readonly DistFile[]): number {
	return files.reduce(
		(total, file) =>
			isServedToBrowser(file.path) && !isJs(file.path) ? total + file.gzipBytes : total,
		0,
	);
}

export function summarize(
	assets: readonly AssetSize[],
	distGzipBytes: number,
	unclassifiedJs: readonly string[],
	servedNonJsGzipBytes = 0,
): SizeReport {
	const initialJsGzipBytes = sumGzip(assets, (asset) => asset.class === 'initial-js');
	return {
		assets,
		initialJsGzipBytes,
		asyncJsGzipBytes: sumGzip(assets, (asset) => asset.class === 'async-js'),
		criticalPathGzipBytes: initialJsGzipBytes + servedNonJsGzipBytes,
		distGzipBytes,
		unclassifiedJs,
	};
}

export function breaches(report: SizeReport, budget: SizeBudget): readonly BudgetBreach[] {
	const checks: readonly [string, number, number][] = [
		['initial JS', report.initialJsGzipBytes, budget.initialJsGzipBytes],
		['largest async chunk', largestAsyncChunk(report), budget.asyncChunkGzipBytes],
		['dist total', report.distGzipBytes, budget.distGzipBytes],
	];
	return checks
		.filter(([, actual, limit]) => actual > limit)
		.map(([label, actual, limit]) => ({ label, actualBytes: actual, budgetBytes: limit }));
}

export function largestAsyncChunk(report: SizeReport): number {
	return report.assets
		.filter((asset) => asset.class === 'async-js')
		.reduce((largest, asset) => Math.max(largest, asset.gzipBytes), 0);
}

export function kib(bytes: number): string {
	return `${(bytes / 1024).toFixed(1)} KiB`;
}
