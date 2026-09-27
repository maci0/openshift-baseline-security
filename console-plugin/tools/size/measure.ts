// Measure what the browser downloads from dist/.
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
	return files.reduce((total, file) => total + file.gzipBytes, 0);
}

export function summarize(
	assets: readonly AssetSize[],
	distGzipBytes: number,
	unclassifiedJs: readonly string[],
): SizeReport {
	return {
		assets,
		initialJsGzipBytes: sumGzip(assets, (asset) => asset.class === 'initial-js'),
		asyncJsGzipBytes: sumGzip(assets, (asset) => asset.class === 'async-js'),
		distGzipBytes,
		unclassifiedJs,
	};
}

export function breaches(report: SizeReport, budget: SizeBudget): readonly BudgetBreach[] {
	const checks: readonly [string, number, number][] = [
		['initial JS (critical path)', report.initialJsGzipBytes, budget.initialJsGzipBytes],
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
