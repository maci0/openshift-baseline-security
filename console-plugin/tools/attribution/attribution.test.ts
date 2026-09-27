import * as fs from 'node:fs';
import * as path from 'node:path';

import { classifyLicense, PERMISSIVE_SPDX } from './spdx';
import { collectNotices, findProjectRoot, readProjectVersion, renderNotices } from './collect';

const FIXTURE_ROOT = path.resolve(__dirname, '../../../.scratch/attribution-fixture');

function writePackage(dir: string, manifest: Record<string, unknown>, licenseFile?: string): void {
	fs.mkdirSync(dir, { recursive: true });
	fs.writeFileSync(path.join(dir, 'package.json'), JSON.stringify(manifest));
	if (licenseFile !== undefined) {
		fs.writeFileSync(path.join(dir, licenseFile), 'fixture license text');
	}
}

function makeFixture(): void {
	fs.rmSync(FIXTURE_ROOT, { force: true, recursive: true });
	const modules = path.join(FIXTURE_ROOT, 'node_modules');
	writePackage(path.join(FIXTURE_ROOT), { name: 'fixture', version: '1.0.0' });
	fs.writeFileSync(path.join(FIXTURE_ROOT, 'yarn.lock'), '');
	writePackage(path.join(modules, 'react'), { name: 'react', version: '18.3.1', license: 'MIT' }, 'LICENSE');
	writePackage(
		path.join(modules, '@patternfly', 'react-core'),
		{ name: '@patternfly/react-core', version: '6.4.3', license: 'MIT' },
		'LICENSE.txt',
	);
	writePackage(
		path.join(modules, 'react', 'node_modules', 'loose-envify'),
		{ name: 'loose-envify', version: '1.4.0', license: 'MIT' },
		'LICENSE',
	);
	writePackage(path.join(modules, 'copyleft-bait'), { name: 'copyleft-bait', version: '0.1.0', license: 'GPL-3.0-only' }, 'LICENSE');
	writePackage(path.join(modules, 'mystery'), { name: 'mystery', version: '2.0.0' }, 'LICENSE');
	writePackage(path.join(modules, 'terse'), { name: 'terse', version: '1.0.0', license: 'SEE LICENSE IN COPYING' }, 'COPYING');
}

describe('classifyLicense', () => {
	it('accepts an SPDX identifier on the allowlist', () => {
		expect(classifyLicense('MIT').ids).toEqual(['MIT']);
	});

	it('accepts every branch of a dual grant', () => {
		const verdict = classifyLicense('MIT OR Apache-2.0');
		expect(verdict.reason).toBe('');
		expect(verdict.ids).toHaveLength(2);
	});

	it('reads the other spellings of a dual grant', () => {
		expect(classifyLicense('MIT/Apache-2.0').reason).toBe('');
		expect(classifyLicense('(MIT OR CC0-1.0)').reason).toBe('');
	});

	it('rejects a copyleft term', () => {
		expect(classifyLicense('GPL-3.0-only').restricted).toBe(true);
		expect(classifyLicense('MPL-2.0').restricted).toBe(true);
		expect(classifyLicense('SSPL-1.0').restricted).toBe(true);
	});

	it('rejects copyleft named only in the free-text form', () => {
		expect(classifyLicense('GNU Affero General Public License v3').restricted).toBe(true);
		expect(classifyLicense('European Union Public Licence 1.2').restricted).toBe(true);
	});

	it('rejects an identifier nobody has read here', () => {
		expect(classifyLicense('WTFPL-2.0').reason).toContain('unapproved identifier');
	});

	it('rejects a pointer instead of an identifier', () => {
		expect(classifyLicense('SEE LICENSE IN LICENSE.md').reason).toContain('names a file');
	});

	it('reports a missing field rather than guessing', () => {
		expect(classifyLicense(undefined).reason).toContain('no license field');
	});

	it('keeps the allowlist free of restricted terms', () => {
		for (const id of PERMISSIVE_SPDX) {
			expect(classifyLicense(id).reason).toBe('');
		}
	});
});

describe('collectNotices', () => {
	beforeAll(makeFixture);
	afterAll(() => {
		fs.rmSync(FIXTURE_ROOT, { force: true, recursive: true });
	});

	it('walks hoisted, scoped, and nested packages', () => {
		const report = collectNotices(FIXTURE_ROOT);
		const names = report.notices.map((notice) => notice.name);
		expect(names).toEqual([
			'@patternfly/react-core',
			'copyleft-bait',
			'loose-envify',
			'mystery',
			'react',
			'terse',
		]);
	});

	it('fails the build on a copyleft package, a missing field, and a file pointer', () => {
		const failures = collectNotices(FIXTURE_ROOT).failures.join('\n');
		expect(failures).toContain('copyleft-bait@0.1.0');
		expect(failures).toContain('mystery@2.0.0: no license field');
		expect(failures).toContain('terse@1.0.0');
	});

	it('does not flag a package that declares an approved license and ships it', () => {
		const failures = collectNotices(FIXTURE_ROOT).failures;
		expect(failures.some((failure) => failure.startsWith('react@18.3.1'))).toBe(false);
	});

	it('reports the license file name so the grant can be reproduced', () => {
		const report = collectNotices(FIXTURE_ROOT);
		const react = report.notices.find((notice) => notice.name === 'react');
		expect(react?.licenseFile).toBe('LICENSE');
	});

	it('fails rather than emitting an empty grant set when nothing is installed', () => {
		const empty = path.resolve(__dirname, '../../../.scratch/attribution-empty');
		fs.mkdirSync(empty, { recursive: true });
		try {
			expect(collectNotices(empty).failures.join('\n')).toContain('yarn install');
		} finally {
			fs.rmSync(empty, { force: true, recursive: true });
		}
	});
});

describe('renderNotices', () => {
	it('names every package, its identifier, and the regeneration command', () => {
		const report = collectNotices(FIXTURE_ROOT);
		const text = renderNotices(report, '9.9.9');
		expect(text).toContain('baseline-security-console-plugin 9.9.9');
		expect(text).toContain('react@18.3.1');
		expect(text).toContain('License:    MIT');
		expect(text).toContain('yarn licenses');
	});

	it('is byte-identical across runs', () => {
		const report = collectNotices(FIXTURE_ROOT);
		expect(renderNotices(report, '1.0.0')).toBe(renderNotices(report, '1.0.0'));
	});
});

describe('findProjectRoot', () => {
	it('walks up to the directory holding package.json and yarn.lock', () => {
		expect(findProjectRoot(path.join(FIXTURE_ROOT, 'node_modules', 'react'))).toBe(FIXTURE_ROOT);
	});
});

describe('readProjectVersion', () => {
	it('reads the version the shipped notices are stamped with', () => {
		expect(readProjectVersion(FIXTURE_ROOT)).toBe('1.0.0');
	});
});
