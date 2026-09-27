import * as fs from 'node:fs';
import * as path from 'node:path';

import {
	collectNotices,
	findProjectRoot,
	OUTPUT_RELATIVE_PATH,
	readProjectVersion,
	renderNotices,
} from './collect';

function main(): void {
	const projectRoot = findProjectRoot(__dirname);
	if (projectRoot === undefined) {
		process.stderr.write('could not locate the console-plugin project root\n');
		process.exitCode = 1;
		return;
	}

	const report = collectNotices(projectRoot);
	const outPath = path.join(projectRoot, OUTPUT_RELATIVE_PATH);
	fs.mkdirSync(path.dirname(outPath), { recursive: true });
	fs.writeFileSync(outPath, renderNotices(report, readProjectVersion(projectRoot)));

	if (report.failures.length > 0) {
		process.stderr.write(
			`${report.failures.length} package(s) cannot be redistributed under the project license:\n`,
		);
		for (const failure of report.failures) {
			process.stderr.write(`  ${failure}\n`);
		}
		process.exitCode = 1;
		return;
	}
	process.stdout.write(
		`wrote ${path.relative(projectRoot, outPath)} (${report.notices.length} packages)\n`,
	);
}

main();
