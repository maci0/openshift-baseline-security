import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'path';
import { loadDotEnv, parseDotEnv } from './dotenv';

const F = 'console-plugin/.env';

const write = (body: string): string => {
  const file = path.join(mkdtempSync(path.join(tmpdir(), 'dotenv-')), '.env');
  writeFileSync(file, body);
  return file;
};

const withEnv = <T>(key: string, value: string | undefined, fn: () => T): T => {
  const prev = process.env[key];
  if (value === undefined) delete process.env[key];
  else process.env[key] = value;
  try {
    return fn();
  } finally {
    if (prev === undefined) delete process.env[key];
    else process.env[key] = prev;
  }
};

describe('parseDotEnv', () => {
  it('reads the allowed keys, skipping comments and blank lines', () => {
    const values = parseDotEnv(
      '# note\n\nCONSOLE_URL=https://console.example.com\nKUBEADMIN_USER=kubeadmin\n',
      F,
    );
    expect(values.get('CONSOLE_URL')).toBe('https://console.example.com');
    expect(values.get('KUBEADMIN_USER')).toBe('kubeadmin');
  });

  it('accepts export and strips a shell-style trailing comment', () => {
    const values = parseDotEnv(
      'export SCREENSHOT_DIR=../docs/screenshots # generated\n',
      F,
    );
    expect(values.get('SCREENSHOT_DIR')).toBe('../docs/screenshots');
  });

  it('keeps spaces and # inside a quoted value', () => {
    const values = parseDotEnv('KUBEADMIN_PASSWORD="pa ss#word"\n', F);
    expect(values.get('KUBEADMIN_PASSWORD')).toBe('pa ss#word');
  });

  it('keeps an unquoted # that is not preceded by a space', () => {
    expect(parseDotEnv('KUBEADMIN_PASSWORD=pa#ss\n', F).get('KUBEADMIN_PASSWORD')).toBe(
      'pa#ss',
    );
  });

  it('rejects a misspelled key instead of dropping it', () => {
    expect(() => parseDotEnv('CONSOLE_URRL=https://console.example.com\n', F)).toThrow(
      /unknown key "CONSOLE_URRL"/,
    );
  });

  it('rejects a line that is not KEY=value', () => {
    expect(() => parseDotEnv('CONSOLE_URL\n', F)).toThrow(/not a KEY=value line/);
  });

  it('rejects a duplicate key and names the first occurrence', () => {
    expect(() => parseDotEnv('KUBEADMIN_USER=a\nKUBEADMIN_USER=b\n', F)).toThrow(
      /duplicate key "KUBEADMIN_USER" \(first set at console-plugin\/\.env:1\)/,
    );
  });

  it('rejects an unterminated quote', () => {
    expect(() => parseDotEnv('KUBEADMIN_PASSWORD="pa ss\n', F)).toThrow(
      /unterminated quote/,
    );
  });

  it('reports every bad line at once, with line numbers', () => {
    let message = '';
    try {
      parseDotEnv('CONSOLE_URRL=x\nJUNK\n', F);
    } catch (e) {
      message = e instanceof Error ? e.message : String(e);
    }
    expect(message).toContain('.env:1');
    expect(message).toContain('.env:2');
  });
});

describe('loadDotEnv', () => {
  it('is a no-op when the file does not exist', () => {
    withEnv('KUBEADMIN_USER', undefined, () => {
      loadDotEnv(path.join(tmpdir(), 'no-such-dir-for-dotenv', '.env'));
      expect(process.env.KUBEADMIN_USER).toBeUndefined();
    });
  });

  it('prefers a non-empty process env over the file', () => {
    const file = write('KUBEADMIN_USER=from-file\n');
    withEnv('KUBEADMIN_USER', 'from-env', () => {
      loadDotEnv(file);
      expect(process.env.KUBEADMIN_USER).toBe('from-env');
    });
  });

  it('fills a key from the file when the process env is empty', () => {
    const file = write('KUBEADMIN_USER=from-file\n');
    withEnv('KUBEADMIN_USER', '   ', () => {
      loadDotEnv(file);
      expect(process.env.KUBEADMIN_USER).toBe('from-file');
    });
  });
});
