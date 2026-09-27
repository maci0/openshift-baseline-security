import { codePointLength, compareForDisplay, foldForSearch, foldSearchQuery, formatList, listSeparators, matchesFolded, textCollator } from './text';

describe('foldForSearch', () => {
  it('folds case and strips diacritics', () => {
    expect(foldForSearch('Sécurité')).toBe('securite');
    expect(foldForSearch('CIS')).toBe('cis');
  });

  // NFD-normalized input must fold to the same string as its composed form,
  // or a name that reached the catalog decomposed matches nothing. The second
  // literal is written as explicit combining escapes so it really is NFD and
  // not a second copy of the composed form.
  it('normalizes decomposed and composed input to the same form', () => {
    const composed = 'Sécurité';
    const decomposed = 'Se\u0301curite\u0301';
    expect(decomposed).not.toBe(composed);
    expect(foldForSearch(composed)).toBe('securite');
    expect(foldForSearch(decomposed)).toBe('securite');
  });

  // The distinction toLocaleLowerCase would collapse: in a search box 'I' and
  // 'i' are one letter in two cases, 'ı' is a different letter.
  it('folds the Turkish dotted and dotless I the way a search box must', () => {
    expect(foldForSearch('I')).toBe('i');
    expect(foldForSearch('İ')).toBe('i');
    expect(foldForSearch('ı')).toBe('ı');
  });
});

// The typeahead folds the catalog once per catalog change and the query once per
// keystroke, so these cases run over the two pre-folded halves: the option is
// folded as it is added to the catalog, the query as it is typed, and only the
// substring test runs per option.
const search = (option: string, query: string): boolean =>
  matchesFolded(foldForSearch(option), foldSearchQuery(query));

describe('matchesFolded over pre-folded input', () => {
  it('matches an exact option', () => {
    expect(search('no_empty_passwords', 'no_empty_passwords')).toBe(true);
  });

  it('matches a substring at the start, middle, and end', () => {
    expect(search('no_empty_passwords', 'no_emp')).toBe(true);
    expect(search('no_empty_passwords', 'empty')).toBe(true);
    expect(search('no_empty_passwords', 'passwords')).toBe(true);
  });

  it('ignores case', () => {
    expect(search('ocp4-cis', 'OCP4')).toBe(true);
    expect(search('ocp4-cis', 'Cis')).toBe(true);
  });

  // toLowerCase().includes() misses both of these.
  it('ignores diacritics so an unaccented query finds an accented name', () => {
    expect(search('règles_de_sécurité', 'securite')).toBe(true);
    expect(search('règles_de_sécurité', 'REGLES')).toBe(true);
  });

  it('applies the same case rules in every locale', () => {
    expect(search('Istanbul_kuralı', 'istanbul')).toBe(true);
    expect(search('İzmir', 'izmir')).toBe(true);
  });

  // The dotless ı is a different letter, not another case of i, so a query
  // typed on a Turkish keyboard matches and an English-case query does not.
  it('keeps the Turkish dotless I distinct from the dotted one', () => {
    expect(search('Izmir', 'ızmir')).toBe(false);
    expect(search('ıstanbul_kuralı', 'ıstanbul')).toBe(true);
  });

  it('rejects a query that is not a substring', () => {
    expect(search('no_empty_passwords', 'audit')).toBe(false);
    expect(search('short', 'a much longer query')).toBe(false);
  });

  it('treats a blank or whitespace-only query as match-all', () => {
    expect(foldSearchQuery('')).toBe('');
    expect(foldSearchQuery('   ')).toBe('');
    expect(search('anything', '')).toBe(true);
    expect(search('anything', '   ')).toBe(true);
  });

  it('trims the query', () => {
    expect(search('no_empty_passwords', '  empty  ')).toBe(true);
  });

  it('matches a substring that abuts an astral character', () => {
    expect(search('a👍b_rule', 'b_rule')).toBe(true);
    expect(search('a👍b_rule', '👍b')).toBe(true);
    expect(search('👍', 'a')).toBe(false);
  });

  // The catalog holds Compliance Operator rule names, which are CJK in a
  // localized cluster; folding must not break a script with no case.
  it('matches in a caseless script', () => {
    expect(search('パッケージ_ルール', 'ケージ')).toBe(true);
    expect(search('правило_без_пароля', 'без_парол')).toBe(true);
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

describe('formatList', () => {
  const ITEMS = ['rule_a', 'rule_b', 'rule_c'];

  it('uses the locale list punctuation, not a literal ", "', () => {
    expect(formatList(ITEMS, 'en')).toBe('rule_a, rule_b, rule_c');
    // German conjoins the final item.
    expect(formatList(['a', 'b', 'c'], 'de')).toBe('a, b und c');
    // Arabic separates with the Arabic comma, never the ASCII one.
    expect(formatList(ITEMS, 'ar')).toBe('rule_a، وrule_b، وrule_c');
    // Japanese and Chinese have no list punctuation: a space, not a comma.
    expect(formatList(ITEMS, 'ja')).toBe('rule_a rule_b rule_c');
    expect(formatList(['a', 'b', 'c'], 'zh')).toBe('abc');
  });

  it('passes empty and single-item lists through unchanged', () => {
    expect(formatList([], 'en')).toBe('');
    expect(formatList(['only'], 'en')).toBe('only');
  });

  it('falls back to the runtime default on an invalid locale tag', () => {
    expect(formatList(ITEMS, 'not a tag')).toBe(formatList(ITEMS));
    // Underscore form is the one the console can hand us; it must normalize.
    expect(formatList(ITEMS, 'en_US')).toBe(formatList(ITEMS, 'en-US'));
  });
});

describe('listSeparators', () => {
  it('returns one fewer literal than items', () => {
    expect(listSeparators(3, 'en')).toEqual([', ', ', ']);
    expect(listSeparators(2, 'en')).toEqual([', ']);
  });

  // The whole point of the helper: German and Hebrew put their conjunction on
  // the last pair, so a caller that hardcoded ", " got it wrong for those.
  it('carries the locale conjunction on the final pair', () => {
    expect(listSeparators(3, 'de')).toEqual([', ', ' und ']);
    expect(listSeparators(3, 'pl')).toEqual([', ', ' i ']);
  });

  it('returns no separators for locales that need none, or for < 2 items', () => {
    expect(listSeparators(3, 'zh')).toEqual([]);
    expect(listSeparators(1, 'en')).toEqual([]);
    expect(listSeparators(0, 'en')).toEqual([]);
  });

  it('falls back to the runtime default on an invalid locale tag', () => {
    expect(listSeparators(3, 'not a tag')).toEqual(listSeparators(3));
  });

  // listSeparators derives the per-count pattern from two constant-size probes
  // instead of formatting `count` items. These are the locales where the
  // two-item pair is spelled differently from the leading separator (de, he,
  // ar, hi, ro, fa, si), where the middle has no literal of its own (en, sv,
  // fr, lt), where the whole list is a bare space (ja, ko), and where there is
  // no separator at all (zh).
  it('derives the same separators the formatter emits for a two-item pair', () => {
    expect(listSeparators(2, 'de')).toEqual([', ']);
    expect(listSeparators(2, 'ar')).toEqual([' و']);
    expect(listSeparators(2, 'hi')).toEqual([' और ']);
    expect(listSeparators(2, 'ja')).toEqual([' ']);
    expect(listSeparators(2, 'zh')).toEqual([]);
  });

  // The real oracle: whatever the derived list is, it must equal the literals
  // the same locale's ListFormat actually produces for that many items, or a
  // caller interleaving links renders the wrong punctuation.
  it('matches Intl.ListFormat for every count and locale probed', () => {
    const locales = [
      'en', 'de', 'fr', 'es', 'pl', 'he', 'ar', 'ja', 'zh', 'ru', 'pt', 'it',
      'nl', 'sv', 'tr', 'ko', 'hi', 'id', 'vi', 'th', 'cs', 'hu', 'ro', 'lt',
      'fa', 'ur', 'si', 'he-IL', 'ar-EG', 'zh-Hans', 'pt-BR', 'en-GB',
    ];
    for (const locale of locales) {
      const fmt = new Intl.ListFormat(locale, { style: 'long', type: 'unit' });
      for (const n of [2, 3, 4, 5, 6, 9, 17, 64, 300]) {
        const expected = fmt
          .formatToParts(Array.from({ length: n }, (_, i) => String(i)))
          .filter((p) => p.type === 'literal')
          .map((p) => p.value);
        expect(listSeparators(n, locale)).toEqual(expected);
      }
    }
  });
});

describe('codePointLength', () => {
  it('counts an astral character once, not as its two UTF-16 units', () => {
    expect('💩'.length).toBe(2);
    expect(codePointLength('💩')).toBe(1);
    expect(codePointLength('a💩b')).toBe(3);
  });

  // A CRD maxLength is a code-point bound, so 600 emoji is admissible where a
  // UTF-16 code-unit count (1200) would refuse it.
  it('admits a 1024-emoji reason and refuses 1025', () => {
    const admitted = '💩'.repeat(1024);
    const refused = '💩'.repeat(1025);
    expect(codePointLength(admitted)).toBe(1024);
    expect(admitted.length).toBe(2048);
    expect(codePointLength(refused)).toBe(1025);
  });

  // NFD 'e' + combining acute is two code points; the composed form is one.
  it('counts a combining sequence by code point, not by grapheme', () => {
    expect(codePointLength('e\u0301')).toBe(2);
    expect(codePointLength('\u00e9')).toBe(1);
  });

  it('counts a lone surrogate instead of throwing', () => {
    expect(codePointLength('a\ud800b')).toBe(3);
  });

  it('agrees with .length on ASCII', () => {
    expect(codePointLength('no_empty_passwords')).toBe('no_empty_passwords'.length);
    expect(codePointLength('')).toBe(0);
  });
});
