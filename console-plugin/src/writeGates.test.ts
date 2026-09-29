import * as fs from 'fs';
import * as path from 'path';

// The API server is the authorization authority for every console write, so an
// ungated call is not a privilege escalation: the request is authorized against
// the signed-in user either way. What it costs is that a control the user cannot
// use is still offered, and the denial surfaces only after the round trip.
//
// `mayWrite` (./permissions) is the single chokepoint for that, and nothing
// enforced that a new API call goes through it: the only prior coverage is
// mayWrite's own truth table. This pins the coverage instead, so the next read
// or write added without a gate fails here rather than in review.
//
// The check is structural, not semantic: a call is considered guarded when
// a mayWrite call appears between the enclosing top-level declaration and the
// call site. That is the shape every current call has, and it fails closed for
// a new ungated function in a file that has no gate at all.

// Every module under src, not just src/components: a mutation helper parked in
// a non-component module (a shared `patchBaseline`, a custom hook) is still a
// console write, and scoping the walk to components left that default-allowed.
const SRC_DIR = __dirname;
const SKIP_DIRS = new Set(['testing']);
const sourceFiles = (dir: string = SRC_DIR): string[] =>
  fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      return SKIP_DIRS.has(entry.name) ? [] : sourceFiles(full);
    }
    if (!/\.tsx?$/.test(entry.name) || /\.test\.tsx?$/.test(entry.name)) {
      return [];
    }
    return [full];
  });

// k8sGet is in the set with the writes: a fetch of a user-named object (a
// TailoredProfile pre-fill) spends the same access review as the update that
// follows it, and an ungated read of a name from the request is the object-level
// miss the update gate was added to prevent.
const MUTATIONS = /\b(k8sGet|k8sPatch|k8sUpdate|k8sCreate|k8sDelete)\s*\(/;
const GUARD = /\bmayWrite\s*\(/;
const TOP_LEVEL_DECL = /^(export\s+)?(const|function|async function|let)\s/;

const mutationLines = (text: string): number[] =>
  text
    .split('\n')
    .map((line, i) => ({ line, i }))
    .filter(({ line }) => MUTATIONS.test(line) && !line.trimStart().startsWith('//'))
    .map(({ i }) => i);

const guarded = (lines: string[], at: number): boolean => {
  let start = 0;
  for (let i = at - 1; i >= 0; i--) {
    if (TOP_LEVEL_DECL.test(lines[i])) {
      start = i;
      break;
    }
  }
  return lines.slice(start, at).some((line) => GUARD.test(line));
};

describe('write gating', () => {
  const files = sourceFiles();

  it('finds the modules that call the API', () => {
    const writing = files.filter((file) => mutationLines(fs.readFileSync(file, 'utf8')).length > 0);
    expect(writing.length).toBeGreaterThan(0);
  });

  // Pins the walk itself: narrowing it back to src/components would leave a
  // mutation helper in any other module ungated and unnoticed.
  it('covers src root modules and components alike', () => {
    expect(files).toEqual(
      expect.arrayContaining([
        path.join(SRC_DIR, 'permissions.ts'),
        path.join(SRC_DIR, 'components', 'CompliancePage.tsx'),
      ]),
    );
  });

  it.each(files)('%s gates every API read and write through mayWrite', (file) => {
    const lines = fs.readFileSync(file, 'utf8').split('\n');
    for (const at of mutationLines(lines.join('\n'))) {
      expect({ line: at + 1, guarded: guarded(lines, at) }).toEqual({ line: at + 1, guarded: true });
    }
  });
});
