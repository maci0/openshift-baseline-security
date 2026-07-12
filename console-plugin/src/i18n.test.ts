import * as fs from 'fs';
import * as path from 'path';
import { PROFILE_INFO } from './models';

// Every t('…') source key used by the plugin must exist in the English locale
// file (OpenShift console i18n uses English as both key and default value).
// Missing keys render as raw English via i18next fallback, so they work for
// en-only sessions but never enter the translation pipeline.
const LOCALE_FILE = path.join(
  __dirname,
  '..',
  'locales',
  'en',
  'plugin__baseline-security-console-plugin.json',
);

const PLURAL_SUFFIXES = ['_zero', '_one', '_two', '_few', '_many', '_other'] as const;

const localeBases = (locale: Record<string, string>): Set<string> => {
  const bases = new Set<string>();
  for (const key of Object.keys(locale)) {
    let base = key;
    for (const suffix of PLURAL_SUFFIXES) {
      if (key.endsWith(suffix)) {
        base = key.slice(0, -suffix.length);
        break;
      }
    }
    bases.add(base);
  }
  return bases;
};

// Collect string-literal keys from t('…') / t("…") in src (single-line and
// simple multi-line concatenations are out of scope; the codebase uses one
// string literal per call).
const collectTKeys = (srcRoot: string): string[] => {
  const keys: string[] = [];
  const walk = (dir: string) => {
    for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, ent.name);
      if (ent.isDirectory()) {
        walk(p);
        continue;
      }
      if (!ent.name.endsWith('.ts') && !ent.name.endsWith('.tsx')) continue;
      if (ent.name.endsWith('.test.ts') || ent.name.endsWith('.test.tsx')) continue;
      // Drop comments so examples like t('…') in docs do not count as keys.
      const text = fs
        .readFileSync(p, 'utf8')
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .replace(/^\s*\/\/.*$/gm, '');
      for (const m of text.matchAll(/\bt\(\s*(['"])((?:\\.|(?!\1).)*)\1/g)) {
        keys.push(m[2].replace(/\\(['"\\])/g, '$1'));
      }
    }
  };
  walk(srcRoot);
  return keys;
};

describe('i18n locale coverage', () => {
  it('locale file is valid JSON with string values', () => {
    const raw = fs.readFileSync(LOCALE_FILE, 'utf8');
    const locale = JSON.parse(raw) as Record<string, unknown>;
    expect(Object.keys(locale).length).toBeGreaterThan(0);
    for (const [k, v] of Object.entries(locale)) {
      expect(typeof k).toBe('string');
      expect(typeof v).toBe('string');
    }
  });

  it('every t() key exists in the English locale (or as a plural base)', () => {
    const locale = JSON.parse(fs.readFileSync(LOCALE_FILE, 'utf8')) as Record<string, string>;
    const bases = localeBases(locale);
    const used = collectTKeys(path.join(__dirname));
    expect(used.length).toBeGreaterThan(0);

    const missing = [...new Set(used)].filter((k) => !bases.has(k) && !(k in locale)).sort();
    expect(missing).toEqual([]);
  });

  // Profile titles/descriptions reach t() as dynamic keys (t(profileTitle(k)),
  // t(info.description)), so collectTKeys cannot see them. A new CRD profile
  // enum forces a PROFILE_INFO entry (typecheck) but nothing forces the locale
  // entries, so guard them here or new profiles silently render English-only in
  // every translated locale.
  it('every PROFILE_INFO title and description exists in the English locale', () => {
    const locale = JSON.parse(fs.readFileSync(LOCALE_FILE, 'utf8')) as Record<string, string>;
    const missing = Object.values(PROFILE_INFO)
      .flatMap(({ title, description }) => [title, description])
      .filter((k) => !(k in locale))
      .sort();
    expect(missing).toEqual([]);
  });

  // i18next falls back to _other when a form is missing, so a base that only
  // ships _one breaks Arabic/Polish/Russian plurals silently. Require the
  // English source set to include both _one and _other for every plural base.
  it('every plural base has both _one and _other forms', () => {
    const locale = JSON.parse(fs.readFileSync(LOCALE_FILE, 'utf8')) as Record<string, string>;
    const forms = new Map<string, Set<string>>();
    for (const key of Object.keys(locale)) {
      for (const suffix of PLURAL_SUFFIXES) {
        if (key.endsWith(suffix)) {
          const base = key.slice(0, -suffix.length);
          let set = forms.get(base);
          if (!set) {
            set = new Set();
            forms.set(base, set);
          }
          set.add(suffix);
          break;
        }
      }
    }
    const incomplete = [...forms.entries()]
      .filter(([, s]) => !s.has('_one') || !s.has('_other'))
      .map(([base, s]) => `${base} (has ${[...s].sort().join(',')})`)
      .sort();
    expect(incomplete).toEqual([]);
  });

  // Orphan English keys never reach translators as live UI copy and drift from
  // the code (stale supersessions). PROFILE_INFO keys are dynamic (not in t()
  // literals) so count them as used; every other locale entry must appear as a
  // t() key or plural base.
  it('every locale key is referenced by t() or PROFILE_INFO (no orphans)', () => {
    const locale = JSON.parse(fs.readFileSync(LOCALE_FILE, 'utf8')) as Record<string, string>;
    const used = new Set(collectTKeys(path.join(__dirname)));
    for (const { title, description } of Object.values(PROFILE_INFO)) {
      used.add(title);
      used.add(description);
    }
    const orphans = Object.keys(locale)
      .filter((k) => {
        let base = k;
        for (const suffix of PLURAL_SUFFIXES) {
          if (k.endsWith(suffix)) {
            base = k.slice(0, -suffix.length);
            break;
          }
        }
        return !used.has(base) && !used.has(k);
      })
      .sort();
    expect(orphans).toEqual([]);
  });
});
