// Print and gate the transferred size of the built plugin.
//
// Runs after webpack in `yarn build` and from `yarn ci`. Gzip level 9, not the
// nginx serving level 5: the number must be a property of the build alone, so
// the same tree reports the same figure on every runner regardless of the web
// server in front of it. Level 9 is at or below level 5 for every asset here,
// so the report is the conservative end of the range the server may send.
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as zlib from 'node:zlib';

import { SIZE_BUDGET } from './budget';
import type { AssetSize, DistFile } from './measure';
import { breaches, classifyAsset, distGzip, kib, largestAsyncChunk, summarize } from './measure';

const DIST_RELATIVE_PATH = 'dist';
const GZIP_LEVEL = 9;
const JS_SUFFIX = '.js';

function findProjectRoot(start: string): string | undefined {
	let dir = path.resolve(start);
	for (;;) {
		if (fs.existsSync(path.join(dir, 'package.json')) && fs.existsSync(path.join(dir, 'yarn.lock'))) {
			return dir;
		}
		const parent = path.dirname(dir);
		if (parent === dir) {
			return undefined;
		}
		dir = parent;
	}
}

function collect(dir: string, prefix: string): readonly DistFile[] {
	const files: DistFile[] = [];
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		const relative = prefix === '' ? entry.name : `${prefix}/${entry.name}`;
		const full = path.join(dir, entry.name);
		if (entry.isDirectory()) {
			files.push(...collect(full, relative));
			continue;
		}
		if (!entry.isFile()) {
			continue;
		}
		const body = fs.readFileSync(full);
		files.push({
			path: relative,
			rawBytes: body.length,
			gzipBytes: zlib.gzipSync(body, { level: GZIP_LEVEL }).length,
		});
	}
	return files;
}

function isJs(relativePath: string): boolean {
	return relativePath.endsWith(JS_SUFFIX);
}

function measureAssets(files: readonly DistFile[]): {
	assets: readonly AssetSize[];
	unclassifiedJs: readonly string[];
} {
	const assets: AssetSize[] = [];
	const unclassifiedJs: string[] = [];
	for (const file of files) {
		const assetClass = classifyAsset(file.path);
		if (assetClass === undefined) {
			if (isJs(file.path)) {
				unclassifiedJs.push(file.path);
			}
			continue;
		}
		assets.push({ ...file, class: assetClass });
	}
	assets.sort((left, right) => right.gzipBytes - left.gzipBytes);
	return { assets, unclassifiedJs };
}

function printTable(assets: readonly AssetSize[], reportLines: readonly string[]): void {
	process.stdout.write('transferred size, gzip\n');
	for (const line of reportLines) {
		process.stdout.write(`  ${line}\n`);
	}
	for (const asset of assets) {
		const role = asset.class === 'initial-js' ? 'initial' : 'async  ';
		process.stdout.write(`  ${kib(asset.gzipBytes).padStart(9)}  ${role}  ${asset.path}\n`);
	}
}

function budgetLine(label: string, actualBytes: number, budgetBytes: number): string {
	return `${label}: ${kib(actualBytes)} / ${kib(budgetBytes)} budget`;
}

function main(): void {
	const projectRoot = findProjectRoot(__dirname);
	if (projectRoot === undefined) {
		process.stderr.write('could not locate the console-plugin project root\n');
		process.exitCode = 1;
		return;
	}
	const distDir = path.join(projectRoot, DIST_RELATIVE_PATH);
	if (!fs.existsSync(distDir)) {
		process.stderr.write('dist/ is missing; run the webpack build first\n');
		process.exitCode = 1;
		return;
	}
	const files = collect(distDir, '');
	const { assets, unclassifiedJs } = measureAssets(files);
	const report = summarize(assets, distGzip(files), unclassifiedJs);

	// The printed table is the record: it lands in the CI log next to the
	// commit that produced it, and nothing is written into dist/ (the image
	// ships every file under there).
	printTable(assets, [
		budgetLine('initial JS', report.initialJsGzipBytes, SIZE_BUDGET.initialJsGzipBytes),
		budgetLine('largest async chunk', largestAsyncChunk(report), SIZE_BUDGET.asyncChunkGzipBytes),
		budgetLine('dist total', report.distGzipBytes, SIZE_BUDGET.distGzipBytes),
	]);

	const over = breaches(report, SIZE_BUDGET);
	if (over.length > 0) {
		process.stderr.write('bundle size budget exceeded:\n');
		for (const breach of over) {
			process.stderr.write(
				`  ${breach.label}: ${kib(breach.actualBytes)} against a ${kib(breach.budgetBytes)} budget\n`,
			);
		}
		process.stderr.write(
			'raise the ceiling in tools/size/budget.ts with the reason, or move the code out of the initial bundle\n',
		);
		process.exitCode = 1;
	}
	if (report.unclassifiedJs.length > 0) {
		process.stderr.write(
			`no webpack output template produced ${report.unclassifiedJs.join(', ')}; the budget cannot place it\n`,
		);
		process.exitCode = 1;
	}
}

main();
