import { compareForDisplay, foldForSearch, matchesSearch, textCollator } from './text';

describe('foldForSearch', () => {
  it('folds case and strips diacritics', () => {
    expect(foldForSearch('Sécurité')).toBe('securite');
    expect(foldForSearch('CIS')).toBe('cis');
  });

  // NFD-normalized input must fold to the same string as its composed form,
  // or a name that reached the catalog decomposed matches nothing.
  it('normalizes decomposed and composed input to the same form', () => {
    expect(foldForSearch('Sécurité')).toBe(foldForSearch('Sécurité'));
  });

  // The distinction toLocaleLowerCase would collapse: in a search box 'I' and
  // 'i' are one letter in two cases, 'ı' is a different letter.
  it('folds the Turkish dotted and dotless I the way a search box must', () => {
    expect(foldForSearch('I')).toBe('i');
    expect(foldForSearch('İ')).toBe('i');
    expect(foldForSearch('ı')).toBe('ı');
  });
});

describe('matchesSearch', () => {
  it('matches an exact option', () => {
    expect(matchesSearch('no_empty_passwords', 'no_empty_passwords')).toBe(true);
  });

  it('matches a substring at the start, middle, and end', () => {
    expect(matchesSearch('no_empty_passwords', 'no_emp')).toBe(true);
    expect(matchesSearch('no_empty_passwords', 'empty')).toBe(true);
    expect(matchesSearch('no_empty_passwords', 'passwords')).toBe(true);
  });

  it('ignores case', () => {
    expect(matchesSearch('ocp4-cis', 'OCP4')).toBe(true);
    expect(matchesSearch('ocp4-cis', 'Cis')).toBe(true);
  });

  // toLowerCase().includes() misses both of these.
  it('ignores diacritics so an unaccented query finds an accented name', () => {
    expect(matchesSearch('règles_de_sécurité', 'securite')).toBe(true);
    expect(matchesSearch('règles_de_sécurité', 'REGLES')).toBe(true);
  });

  it('applies the same case rules in every locale', () => {
    expect(matchesSearch('Istanbul_kuralı', 'istanbul')).toBe(true);
    expect(matchesSearch('İzmir', 'izmir')).toBe(true);
  });

  // The dotless ı is a different letter, not another case of i, so a query
  // typed on a Turkish keyboard matches and an English-case query does not.
  it('keeps the Turkish dotless I distinct from the dotted one', () => {
    expect(matchesSearch('Izmir', 'ızmir')).toBe(false);
    expect(matchesSearch('ıstanbul_kuralı', 'ıstanbul')).toBe(true);
  });

  it('rejects a query that is not a substring', () => {
    expect(matchesSearch('no_empty_passwords', 'audit')).toBe(false);
    expect(matchesSearch('short', 'a much longer query')).toBe(false);
  });

  it('treats a blank or whitespace-only query as match-all', () => {
    expect(matchesSearch('anything', '')).toBe(true);
    expect(matchesSearch('anything', '   ')).toBe(true);
  });

  it('trims the query', () => {
    expect(matchesSearch('no_empty_passwords', '  empty  ')).toBe(true);
  });

  it('matches a substring that abuts an astral character', () => {
    expect(matchesSearch('a👍b_rule', 'b_rule')).toBe(true);
    expect(matchesSearch('a👍b_rule', '👍b')).toBe(true);
    expect(matchesSearch('👍', 'a')).toBe(false);
  });

  // The catalog holds Compliance Operator rule names, which are CJK in a
  // localized cluster; folding must not break a script with no case.
  it('matches in a caseless script', () => {
    expect(matchesSearch('パッケージ_ルール', 'ケージ')).toBe(true);
    expect(matchesSearch('правило_без_пароля', 'без_парол')).toBe(true);
  });
});

describe('compareForDisplay', () => {
  it('is a total order over distinct names', () => {
    expect(compareForDisplay('a', 'b', 'en')).toBeLessThan(0);
    expect(compareForDisplay('b', 'a', 'en')).toBeGreaterThan(0);
    expect(compareForDisplay('a', 'a', 'en')).toBe(0);
  });

  // numeric: true from textCollator, not byte order ('_' is 0x5f, above '9').
  it('orders embedded numbers the way a reader expects', () => {
    const sorted = ['rule_10', 'rule_2'].sort((a, b) => compareForDisplay(a, b, 'en'));
    expect(sorted).toEqual(['rule_2', 'rule_10']);
  });

  it('sorts an accented name beside its base letter', () => {
    const sorted = ['sécurité', 'sauvegarde', 'systeme'].sort((a, b) =>
      compareForDisplay(a, b, 'en'),
    );
    expect(sorted).toEqual(['sauvegarde', 'sécurité', 'systeme']);
  });

  // Byte order sorts by code point, which puts Arabic and Hebrew before Latin
  // and reverses the Arabic abjad; the locale's collation puts Arabic first and
  // orders ب before ت, as abjad requires.
  it('follows the locale collation rather than byte order', () => {
    const sorted = ['a', 'ب', 'ت'].sort((x, y) => compareForDisplay(x, y, 'ar'));
    expect(sorted).toEqual(['ب', 'ت', 'a']);
  });

  it('does not throw on an invalid locale tag', () => {
    expect(compareForDisplay('a', 'b', 'en_US_BAD')).toBeLessThan(0);
  });
});

describe('textCollator', () => {
  it('returns a cached collator per locale tag', () => {
    expect(textCollator('en')).toBe(textCollator('en'));
    // safeLocale normalizes the underscore form onto the same cache entry.
    expect(textCollator('en_US')).toBe(textCollator('en-US'));
  });

  // new Intl.Collator('') throws RangeError, so the runtime-default collator
  // must be built with undefined and cached under the empty key.
  it('caches the runtime-default collator under the empty tag', () => {
    expect(textCollator(undefined)).toBe(textCollator());
  });
});
