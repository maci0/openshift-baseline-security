import * as fs from 'node:fs';
import * as path from 'node:path';

import { classifyLicense, PERMISSIVE_SPDX, RESTRICTED_SPDX } from './spdx';

const LICENSE_FILE_NAMES: readonly string[] = [
	'LICENSE',
	'LICENCE',
	'LICENSE.md',
	'LICENSE.txt',
	'LICENCE.md',
	'LICENCE.txt',
	'COPYING',
	'NOTICE',
];

const OUTPUT_RELATIVE_PATH = path.join('dist', 'THIRD-PARTY-NOTICES.txt');

export interface PackageNotice {
	readonly name: string;
	readonly version: string;
	readonly ids: readonly string[];
	readonly licenseFile: string;
	readonly reason: string;
	readonly restricted: boolean;
}

export interface NoticeReport {
	readonly notices: readonly PackageNotice[];
	readonly failures: readonly string[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null;
}

function sortKey(notice: PackageNotice): string {
	return `${notice.name}@${notice.version}`;
}

function compareCodePoints(left: string, right: string): number {
	if (left < right) {
		return -1;
	}
	return left > right ? 1 : 0;
}

function readPackageName(dir: string): string {
	const parent = path.basename(path.dirname(dir));
	return parent.startsWith('@') ? `${parent}/${path.basename(dir)}` : path.basename(dir);
}

function findLicenseFile(dir: string): string {
	return LICENSE_FILE_NAMES.find((name) => fs.existsSync(path.join(dir, name))) ?? 'MISSING';
}

// Legacy `package.json` carried `licenses: [{type, url}]`; the field is gone from
// the manifest schema but still present in older published tarballs.
function readLicenseField(manifest: Record<string, unknown>): string | undefined {
	const field = manifest.license;
	if (typeof field === 'string') {
		return field;
	}
	if (isRecord(field) && typeof field.type === 'string') {
		return field.type;
	}
	const legacy = manifest.licenses;
	if (Array.isArray(legacy) && legacy.length > 0) {
		const first: unknown = legacy[0];
		if (isRecord(first) && typeof first.type === 'string') {
			return first.type;
		}
	}
	return undefined;
}

function readPackage(dir: string): PackageNotice | undefined {
	const manifestPath = path.join(dir, 'package.json');
	if (!fs.existsSync(manifestPath)) {
		return undefined;
	}
	const parsed: unknown = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
	if (!isRecord(parsed)) {
		return undefined;
	}
	const version = typeof parsed.version === 'string' ? parsed.version : 'UNKNOWN';
	const verdict = classifyLicense(readLicenseField(parsed));
	return {
		name: readPackageName(dir),
		version,
		ids: verdict.ids,
		licenseFile: findLicenseFile(dir),
		reason: verdict.reason,
		restricted: verdict.restricted,
	};
}

// Yarn's node-modules linker hoists and only nests on a version clash, so a
// scope-unaware single-level walk would miss the nested copies.
function listPackageDirs(modulesDir: string): readonly string[] {
	if (!fs.existsSync(modulesDir)) {
		return [];
	}
	const dirs: string[] = [];
	for (const entry of fs.readdirSync(modulesDir, { withFileTypes: true })) {
		if (entry.name.startsWith('.')) {
			continue;
		}
		const entryPath = path.join(modulesDir, entry.name);
		if (!entry.isDirectory() || entry.isSymbolicLink()) {
			continue;
		}
		if (entry.name.startsWith('@')) {
			for (const scoped of fs.readdirSync(entryPath, { withFileTypes: true })) {
				if (scoped.isDirectory() && !scoped.isSymbolicLink()) {
					dirs.push(path.join(entryPath, scoped.name));
				}
			}
			continue;
		}
		dirs.push(entryPath);
	}
	return dirs;
}

function walk(modulesDir: string, seen: Set<string>, out: PackageNotice[]): void {
	for (const dir of listPackageDirs(modulesDir)) {
		const real = fs.realpathSync(dir);
		if (seen.has(real)) {
			continue;
		}
		seen.add(real);
		const notice = readPackage(dir);
		if (notice !== undefined) {
			out.push(notice);
		}
		walk(path.join(dir, 'node_modules'), seen, out);
	}
}

export function collectNotices(projectRoot: string): NoticeReport {
	const notices: PackageNotice[] = [];
	const modulesDir = path.join(projectRoot, 'node_modules');
	walk(modulesDir, new Set<string>(), notices);
	// Codepoint order, not localeCompare: the output lands in a shipped image and
	// must not change with the builder's ICU data.
	notices.sort((left, right) => compareCodePoints(sortKey(left), sortKey(right)));

	// An empty closure means the install never ran, not that nothing is bundled.
	// Emitting a notices file with zero entries would pass silently.
	if (notices.length === 0) {
		return {
			notices,
			failures: [`no packages under ${modulesDir}: run yarn install first`],
		};
	}

	const failures = notices
		.filter((notice) => notice.restricted || notice.licenseFile === 'MISSING' || notice.reason !== '')
		.map((notice) => {
			if (notice.restricted) {
				return `${notice.name}@${notice.version}: ${notice.reason}`;
			}
			if (notice.licenseFile === 'MISSING') {
				return `${notice.name}@${notice.version}: no LICENSE file to reproduce`;
			}
			return `${notice.name}@${notice.version}: ${notice.reason}`;
		});

	return { notices, failures };
}

function licenseLabel(ids: readonly string[]): string {
	return ids.length > 0 ? ids.join(' OR ') : 'UNKNOWN';
}

export function renderNotices(report: NoticeReport, projectVersion: string): string {
	const lines: string[] = [
		'THIRD-PARTY NOTICES',
		'',
		`baseline-security-console-plugin ${projectVersion}`,
		'',
		'This file lists every package installed in the dependency closure of this',
		'plugin, the SPDX identifier each one declares, and the license file that',
		'carries its grant. The webpack bundle in this image redistributes a subset',
		'of them; listing the full closure is deliberate, so a downstream consumer',
		'traces a grant back to its origin without reconstructing the install graph.',
		'',
		'Regenerate with: yarn licenses',
		'',
		'=======================================================================',
		'',
	];

	for (const notice of report.notices) {
		lines.push(`${notice.name}@${notice.version}`);
		lines.push(`  License:    ${licenseLabel(notice.ids)}`);
		lines.push(`  License file: ${notice.licenseFile}`);
		lines.push('');
	}

	lines.push('=======================================================================');
	lines.push('');
	lines.push(`Licenses accepted for redistribution: ${[...PERMISSIVE_SPDX].sort().join(', ')}`);
	lines.push('');
	lines.push(
		`Never acceptable without a license review: ${[...RESTRICTED_SPDX].sort().join(', ')}`,
	);
	lines.push('');

	return lines.join('\n');
}

export function findProjectRoot(start: string): string | undefined {
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

export function readProjectVersion(projectRoot: string): string {
	const manifest: unknown = JSON.parse(
		fs.readFileSync(path.join(projectRoot, 'package.json'), 'utf8'),
	);
	// SAFETY: findProjectRoot only returns a directory that has a readable
	// package.json, and this project's own manifest declares a string version.
	return isRecord(manifest) && typeof manifest.version === 'string' ? manifest.version : 'unknown';
}

export { OUTPUT_RELATIVE_PATH };

