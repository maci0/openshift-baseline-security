// SPDX identifiers this project is willing to redistribute inside the console
// plugin bundle. The project license is Apache-2.0; the shipped bundle mixes in
// whatever the dependency closure brings, so the gate is an allowlist rather
// than a denylist: an identifier nobody has read here is a build failure, not
// a silent grant. Each identifier outside the familiar OSI list carries the
// reason it was read and accepted.
export const PERMISSIVE_SPDX: ReadonlySet<string> = new Set([
	'0BSD',
	'Apache-2.0',
	'BSD-2-Clause',
	'BSD-3-Clause',
	// Blue Oak Council's model license: permissive, no copyleft and no
	// source-availability term. Read for glob, lru-cache, minimatch, minipass,
	// and path-scurry, which the `resolutions` entries pin for the audit fixes.
	'BlueOak-1.0.0',
	// Creative Commons attribution licenses, carried by data packages rather
	// than code (caniuse-lite, spdx-exceptions). Redistribution and modification
	// are permitted with attribution, which the notices file reproduces next to
	// the identifier; there is no copyleft and no non-commercial term.
	'CC-BY-3.0',
	'CC-BY-4.0',
	'CC0-1.0',
	'ISC',
	'MIT',
	'MIT-0',
	'NCSA',
	'PostgreSQL',
	'Unlicense',
	'Zlib',
	'Python-2.0',
]);

// Copyleft and source-available terms that may never be linked into an Apache-2.0
// redistribution without a deliberate license review. Listed for the error text
// only: presence in this set is reported with its identifier, never normalised
// into the permissive path.
export const RESTRICTED_SPDX: ReadonlySet<string> = new Set([
	'AGPL-1.0-only',
	'AGPL-1.0-or-later',
	'AGPL-3.0-only',
	'AGPL-3.0-or-later',
	'CC-BY-NC-4.0',
	'CDDL-1.0',
	'CDDL-1.1',
	'EPL-1.0',
	'EPL-2.0',
	'EUPL-1.2',
	'GPL-2.0-only',
	'GPL-2.0-or-later',
	'GPL-3.0-only',
	'GPL-3.0-or-later',
	'LGPL-2.1-only',
	'LGPL-2.1-or-later',
	'LGPL-3.0-only',
	'LGPL-3.0-or-later',
	'MPL-2.0',
	'SSPL-1.0',
]);

// Substring probes for the license strings that predate SPDX and show up in
// `package.json` verbatim ("SEE LICENSE IN ...", "SEE LICENSE IN LICENSE.md",
// "(MIT OR CC0-1.0)", "BSD*" and friends). Matched lowercase against the whole
// raw field before any identifier is extracted.
const RESTRICTED_PROBES: readonly string[] = [
	'agpl',
	'gpl',
	'lgpl',
	'mpl',
	'epl',
	'eupl',
	'cddl',
	'sspl',
	'commons clause',
	'cc-by-nc',
	'general public license',
	'public licence',
	'affero',
	'noncommercial',
	'non-commercial',
];

// "MIT OR Apache-2.0" and "MIT/Apache-2.0" both spell a choice of grants; the
// separators are not identifiers and must not be read as one.
const LICENSE_OPERATORS = /(?:\s+OR\s+|\s+AND\s+|\/)+/g;

const SPDX_TOKEN = /[A-Za-z0-9][A-Za-z0-9.+-]*/g;

export interface LicenseVerdict {
	readonly ids: readonly string[];
	readonly restricted: boolean;
	readonly reason: string;
}

export function classifyLicense(raw: string | undefined): LicenseVerdict {
	if (raw === undefined || raw.trim() === '') {
		return {
			ids: [],
			restricted: false,
			reason: 'no license field in package.json',
		};
	}

	const lowered = raw.toLowerCase();
	const hit = RESTRICTED_PROBES.find((probe) => lowered.includes(probe));
	if (hit !== undefined) {
		return { ids: [], restricted: true, reason: `raw license "${raw}" is restricted` };
	}

	if (lowered.includes('see license in')) {
		return {
			ids: [],
			restricted: false,
			reason: `raw license "${raw}" names a file instead of an SPDX identifier`,
		};
	}

	const ids = (raw.replace(LICENSE_OPERATORS, ' ').match(SPDX_TOKEN) ?? []).filter(
		(token) => token.toUpperCase() !== 'OR' && token.toUpperCase() !== 'AND',
	);
	if (ids.length === 0) {
		return { ids: [], restricted: false, reason: `raw license "${raw}" is free text` };
	}

	// A single package may offer a choice ("MIT OR Apache-2.0"); every branch has
	// to be a grant this project accepts before the package is redistributable.
	const unknown = ids.filter((id) => !PERMISSIVE_SPDX.has(id));
	if (unknown.length > 0) {
		return {
			ids,
			restricted: false,
			reason: `unapproved identifier${unknown.length > 1 ? 's' : ''}: ${unknown.join(', ')}`,
		};
	}

	return { ids, restricted: false, reason: '' };
}
