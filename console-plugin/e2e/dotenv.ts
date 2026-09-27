// .env loader for the live-console Playwright run.
//
// Precedence: a non-empty process env wins (CI injects secrets; a local .env is
// a convenience), otherwise the file supplies the value. Only E2E_ENV_KEYS are
// read: the runner must not inherit PATH, NODE_OPTIONS, or an unrelated
// variable a developer left in the file. An unknown key is usually a typo of
// one of them (CONSOLE_URRL), and silently dropping it surfaces much later as
// "CONSOLE_URL must be set", so it is rejected here instead.
import { existsSync, readFileSync } from 'node:fs';

// Keys `yarn test-e2e` reads from .env (see .env.example).
export const E2E_ENV_KEYS: ReadonlySet<string> = new Set([
  'CONSOLE_URL',
  'KUBEADMIN_USER',
  'KUBEADMIN_PASSWORD',
  'SCREENSHOT_DIR',
]);

const ALLOWED_LIST = [...E2E_ENV_KEYS].join(', ');

// parseDotEnv reads a .env file and returns the values for allowed keys.
// Every malformed line is reported at once so one run surfaces all of them.
export function parseDotEnv(text: string, file: string): Map<string, string> {
  const values = new Map<string, string>();
  const lines = new Map<string, string>();
  const errors: string[] = [];

  text.split(/\r?\n/).forEach((line, i) => {
    const at = `${file}:${i + 1}`;
    const t = line.trim();
    if (!t || t.startsWith('#')) return;

    const eq = t.indexOf('=');
    if (eq <= 0) {
      errors.push(`${at}: not a KEY=value line: ${t}`);
      return;
    }
    let key = t.slice(0, eq).trim();
    // `export KEY=value` (shell-sourced style) is accepted.
    if (key.startsWith('export ')) key = key.slice(7).trim();
    if (!E2E_ENV_KEYS.has(key)) {
      errors.push(`${at}: unknown key "${key}" (allowed: ${ALLOWED_LIST})`);
      return;
    }
    if (lines.has(key)) {
      errors.push(`${at}: duplicate key "${key}" (first set at ${lines.get(key)})`);
      return;
    }

    const raw = t.slice(eq + 1).trim();
    let val: string;
    if (
      (raw.startsWith('"') && raw.endsWith('"')) ||
      (raw.startsWith("'") && raw.endsWith("'"))
    ) {
      // Quoted: keep interior # and spaces (passwords may contain them).
      val = raw.slice(1, -1);
    } else {
      if (raw.startsWith('"') || raw.startsWith("'")) {
        errors.push(`${at}: unterminated quote in value for "${key}"`);
        return;
      }
      // Unquoted: strip a shell-style trailing comment (`KEY=value # note`).
      // Require a space before # so values like `pass#1` stay intact.
      const hash = raw.indexOf(' #');
      val = hash >= 0 ? raw.slice(0, hash).trimEnd() : raw;
    }
    values.set(key, val);
    lines.set(key, at);
  });

  if (errors.length > 0) {
    throw new Error(`invalid ${file}:\n  ${errors.join('\n  ')}`);
  }
  return values;
}

// loadDotEnv reads a .env file, if present, and writes its values into
// process.env, leaving a non-empty process env (or a key the file does not set)
// alone.
export function loadDotEnv(path: string): void {
  if (!existsSync(path)) return;
  for (const [key, val] of parseDotEnv(readFileSync(path, 'utf8'), path)) {
    const existing = process.env[key];
    if (existing !== undefined && existing.trim() !== '') continue;
    process.env[key] = val;
  }
}
