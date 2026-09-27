import type { SizeBudget } from './budget';
import type { AssetSize } from './measure';
import { breaches, classifyAsset, distGzip, kib, largestAsyncChunk, summarize } from './measure';

const asset = (assetPath: string, gzipBytes: number): AssetSize => ({
	path: assetPath,
	class: classifyAsset(assetPath) ?? 'initial-js',
	rawBytes: gzipBytes * 3,
	gzipBytes,
});

const TIGHT: SizeBudget = {
	initialJsGzipBytes: 1000,
	asyncChunkGzipBytes: 500,
	distGzipBytes: 2000,
};

describe('classifyAsset', () => {
	it('reads the class out of the webpack output name templates', () => {
		expect(classifyAsset('plugin-entry-bundle-abc123.min.js')).toBe('initial-js');
		expect(classifyAsset('results-tab-chunk-def456.min.js')).toBe('async-js');
	});

	it('classifies on the file name, not on a path segment', () => {
		expect(classifyAsset('plugin-entry-bundle-abc123.min.js')).toBe('initial-js');
		expect(classifyAsset('dist-bundle-copy/report-chunk-abc123.min.js')).toBe('async-js');
		expect(classifyAsset('locales/en/plugin__baseline-security-console-plugin.json')).toBeUndefined();
	});

	it('returns undefined for JS the budget does not understand', () => {
		expect(classifyAsset('runtime.js')).toBeUndefined();
	});
});

describe('summarize', () => {
	const report = summarize(
		[asset('plugin-entry-bundle-1.min.js', 300), asset('overview-charts-chunk-2.min.js', 200)],
		700,
		[],
	);

	it('adds the initial JS across every entry bundle', () => {
		expect(report.initialJsGzipBytes).toBe(300);
	});

	it('adds the async chunks separately from the critical path', () => {
		expect(report.asyncJsGzipBytes).toBe(200);
		expect(largestAsyncChunk(report)).toBe(200);
	});

	it('keeps the whole dist tree in the total', () => {
		expect(report.distGzipBytes).toBe(700);
	});

	it('totals every file, JS or not', () => {
		expect(
			distGzip([asset('plugin-entry-bundle-1.min.js', 10), { path: 'x.json', rawBytes: 20, gzipBytes: 5 }]),
		).toBe(15);
	});
});

describe('breaches', () => {
	it('passes a tree inside every ceiling', () => {
		const report = summarize(
			[asset('plugin-entry-bundle-1.min.js', 900), asset('c-chunk-2.min.js', 400)],
			1500,
			[],
		);
		expect(breaches(report, TIGHT)).toEqual([]);
	});

	it('names the ceiling an oversized entry bundle broke', () => {
		const report = summarize([asset('plugin-entry-bundle-1.min.js', 1001)], 1001, []);
		expect(breaches(report, TIGHT).map((breach) => breach.label)).toEqual([
			'initial JS (critical path)',
		]);
	});

	it('checks the largest async chunk, not the sum of them', () => {
		const report = summarize(
			[asset('a-chunk-1.min.js', 400), asset('b-chunk-2.min.js', 400)],
			800,
			[],
		);
		expect(breaches(report, TIGHT)).toEqual([]);
	});

	it('reports a JS file no output template produced, outside the numeric gates', () => {
		const report = summarize([asset('plugin-entry-bundle-1.min.js', 100)], 100, ['runtime.js']);
		expect(breaches(report, TIGHT)).toEqual([]);
		expect(report.unclassifiedJs).toEqual(['runtime.js']);
	});
});

describe('kib', () => {
	it('reports one decimal place', () => {
		expect(kib(1024)).toBe('1.0 KiB');
		expect(kib(1536)).toBe('1.5 KiB');
	});
});
