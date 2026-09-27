import type { SizeBudget } from './budget';
import type { AssetSize, DistFile } from './measure';
import {
	breaches,
	classifyAsset,
	distGzip,
	isServedToBrowser,
	kib,
	largestAsyncChunk,
	servedNonJsGzip,
	summarize,
} from './measure';

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

	it('keeps the served tree in the total', () => {
		expect(report.distGzipBytes).toBe(700);
	});

	it('adds the served non-JS a first paint waits on', () => {
		const withLocales = summarize([asset('plugin-entry-bundle-1.min.js', 300)], 700, [], 40);
		expect(withLocales.criticalPathGzipBytes).toBe(340);
		expect(report.criticalPathGzipBytes).toBe(300);
	});

	it('totals every served file, JS or not', () => {
		expect(
			distGzip([asset('plugin-entry-bundle-1.min.js', 10), { path: 'x.json', rawBytes: 20, gzipBytes: 5 }]),
		).toBe(15);
	});

	it('leaves the license notice out of the served total', () => {
		expect(isServedToBrowser('THIRD-PARTY-NOTICES.txt')).toBe(false);
		expect(isServedToBrowser('locales/en/plugin__baseline-security-console-plugin.json')).toBe(true);
		const files: readonly DistFile[] = [
			{ path: 'plugin-entry-bundle-1.min.js', rawBytes: 30, gzipBytes: 10 },
			{ path: 'THIRD-PARTY-NOTICES.txt', rawBytes: 3000, gzipBytes: 900 },
		];
		expect(distGzip(files)).toBe(10);
	});

	it('counts the manifest and locales as first-paint bytes, the notice as neither', () => {
		const files: readonly DistFile[] = [
			{ path: 'plugin-manifest.json', rawBytes: 20, gzipBytes: 6 },
			{ path: 'locales/en/plugin__baseline-security-console-plugin.json', rawBytes: 80, gzipBytes: 14 },
			{ path: 'THIRD-PARTY-NOTICES.txt', rawBytes: 3000, gzipBytes: 900 },
		];
		expect(servedNonJsGzip(files)).toBe(20);
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
			'initial JS',
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
