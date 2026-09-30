import * as fs from 'node:fs';
import * as path from 'node:path';

import { classifyLicense, PERMISSIVE_SPDX, RESTRICTED_SPDX } from './spdx';
import { isString } from '../../src/parse';
import type { UntrustedValue } from '../../src/parse';

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

// The `package.json` fields the notices file reproduces. A published tarball's
// manifest is untrusted JSON, so the fields are declared here and every read
// below still narrows through a guard.
interface PackageManifest {
	readonly license?: UntrustedValue;
	readonly version?: UntrustedValue;
	readonly licenses?: UntrustedValue;
}

// The `license: { type }` object spelling and each element of the legacy
// `licenses: [{ type }]` array.
interface LicenseTypeField {
	readonly type?: UntrustedValue;
}

const asPackageManifest = (value: UntrustedValue): value is PackageManifest =>
	typeof value === 'object' && value !== null;

const asLicenseTypeField = (value: UntrustedValue): value is LicenseTypeField =>
	typeof value === 'object' && value !== null;

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

// Publishers spell the shipped file as npm's canonical lowercase `license` as
// often as they do `LICENSE`, and the case is not significant to a reader.
// Compare case-insensitively but in the candidate order above, so the file
// picked is the same on every runner, and report the spelling the tarball
// actually ships. A package that ships none is reported as such rather than as
// a missing file: the declared identifier is then the only record of the grant.
export const NO_LICENSE_FILE = '(no file shipped)';

function findLicenseFile(dir: string): string {
	let entries: readonly string[];
	try {
		entries = fs.readdirSync(dir);
	} catch {
		return NO_LICENSE_FILE;
	}
	for (const name of LICENSE_FILE_NAMES) {
		const lowered = name.toLowerCase();
		const match = entries.find((entry) => entry.toLowerCase() === lowered);
		if (match !== undefined) {
			return match;
		}
	}
	return NO_LICENSE_FILE;
}

// Legacy `package.json` carried `licenses: [{type, url}]`; the field is gone from
// the manifest schema but still present in older published tarballs.
function readLicenseField(manifest: PackageManifest): string | undefined {
	const field = manifest.license;
	if (isString(field)) {
		return field;
	}
	if (asLicenseTypeField(field) && isString(field.type)) {
		return field.type;
	}
	const legacy = manifest.licenses;
	if (Array.isArray(legacy) && legacy.length > 0) {
		const first: UntrustedValue = legacy[0];
		if (asLicenseTypeField(first) && isString(first.type)) {
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
	const parsed: UntrustedValue = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
	if (!asPackageManifest(parsed)) {
		return undefined;
	}
	const version = isString(parsed.version) ? parsed.version : 'UNKNOWN';
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

// bun's hoisted linker (bunfig.toml) hoists and only nests on a version clash, so a
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
			failures: [`no packages under ${modulesDir}: run bun install first`],
		};
	}

	// A package that ships no license file is still redistributable when its
	// manifest declares an identifier this project accepts: the notice records
	// "(no file shipped)" and the declared grant is the only record. Anything
	// else fails, and `reason` is what says why: a copyleft term, an identifier
	// nobody has read here, free text, or no license field at all.
	const failures = notices
		.filter((notice) => notice.restricted || notice.reason !== '')
		.map((notice) => `${notice.name}@${notice.version}: ${notice.reason}`);

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
		`Where a package ships no license file at all, the entry reads ${NO_LICENSE_FILE}`,
		'and the identifier its manifest declares is the only record of its grant.',
		'',
		'Regenerate with: bun run licenses',
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
		if (fs.existsSync(path.join(dir, 'package.json')) && fs.existsSync(path.join(dir, 'bun.lock'))) {
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
	const manifest: UntrustedValue = JSON.parse(
		fs.readFileSync(path.join(projectRoot, 'package.json'), 'utf8'),
	);
	// findProjectRoot only returns a directory that has a readable package.json,
	// and this project's own manifest declares a string version; anything else
	// falls back rather than stamping the notices file with a wrong version.
	return asPackageManifest(manifest) && isString(manifest.version)
		? manifest.version
		: 'unknown';
}

export { OUTPUT_RELATIVE_PATH };

