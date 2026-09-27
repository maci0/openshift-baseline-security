import * as fs from 'fs';
import * as path from 'path';

// The API server is the authorization authority for every console write, so an
// ungated call is not a privilege escalation: the request is authorized against
// the signed-in user either way. What it costs is that a control the user cannot
// use is still offered, and the denial surfaces only after the round trip.
//
// `mayWrite` (./permissions) is the single chokepoint for that, and nothing
// enforced that a new mutation goes through it: the only prior coverage is
// mayWrite's own truth table. This pins the coverage instead, so the next write
// added to a component without a gate fails here rather than in review.
//
// The check is structural, not semantic: a mutation is considered guarded when
// a mayWrite call appears between the enclosing top-level declaration and the
// call site. That is the shape every current write has, and it fails closed for
// a new ungated function in a file that has no gate at all.

const COMPONENTS_DIR = path.join(__dirname, 'components');

const MUTATIONS = /\b(k8sPatch|k8sUpdate|k8sCreate|k8sDelete)\s*\(/;
const GUARD = /\bmayWrite\s*\(/;
const TOP_LEVEL_DECL = /^(export\s+)?(const|function|async function|let)\s/;

const sourceFiles = (): string[] =>
  fs
    .readdirSync(COMPONENTS_DIR)
    .filter((name) => name.endsWith('.tsx') || name.endsWith('.ts'))
    .map((name) => path.join(COMPONENTS_DIR, name));

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

  it('finds the components that write to the API', () => {
    const writing = files.filter((file) => mutationLines(fs.readFileSync(file, 'utf8')).length > 0);
    expect(writing.length).toBeGreaterThan(0);
  });

  it.each(files)('%s gates every mutation through mayWrite', (file) => {
    const lines = fs.readFileSync(file, 'utf8').split('\n');
    for (const at of mutationLines(lines.join('\n'))) {
      expect({ line: at + 1, guarded: guarded(lines, at) }).toEqual({ line: at + 1, guarded: true });
    }
  });
});
