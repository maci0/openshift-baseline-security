// Locale-aware text matching and display ordering for the typeahead option
// lists (profile and rule pickers), plus the code-point length a CRD maxLength
// is counted in.
//
// Raw toLowerCase() is the wrong fold for user-typed search: it leaves
// diacritics in place, so a user typing "securite" never finds "sécurité", and
// it treats the Turkish dotted capital İ as a two-character sequence that
// matches nothing. foldForSearch strips combining marks after folding, which
// covers both and keeps matching a plain substring test.
//
// Display order is a different problem and uses Intl.Collator: the locale's
// collation decides where letters sit, so an accented name sorts beside its base
// letter instead of after every z, and an Arabic list follows the abjad.

import { intlCached, safeLocale } from './dates';

// Collators are expensive to build and a ~1k-rule catalog is sorted on every
// render, so keep one per locale tag. intlCached caps the map: the tag comes
// from the console session but is not limited to the console's language list, so
// the reuse cannot rest on that assumption.
const collators = new Map<string, Intl.Collator>();

// Construct with the validated tag, never with the empty string: new
// Intl.Collator('') throws RangeError, while undefined means "runtime default".
// The empty key is cache-only.
export const textCollator = (locale?: string): Intl.Collator => {
  const tag = safeLocale(locale);
  const key = tag ?? '';
  return intlCached(collators, key, () =>
    // numeric: true orders rule_2 before rule_10 the way a reader expects,
    // rather than by byte value where '_' (0x5f) sorts above every digit.
    new Intl.Collator(tag, { usage: 'sort', numeric: true }),
  );
};

// Case- and accent-insensitive form of a string for substring search.
//
// toLowerCase, not toLocaleLowerCase: locale-sensitive casing is for display.
// In Turkish it maps 'I' to the dotless 'ı', but in a search box 'I' and 'i'
// are two cases of the same letter while 'ı' is a different letter, so the
// locale mapping would make a query typed in either case miss the option.
// Locale-independent folding is what Unicode's own case folding does.
//
// NFD then exposes combining marks, which are dropped, so 'é' compares equal
// to 'e' and 'İ' (folded to 'i' + U+0307) compares equal to 'i'. Letters that
// are genuinely distinct letters in a locale have no canonical decomposition
// and survive intact, which is what keeps Turkish 'i' and 'ı' apart.
// Folding a name is a toLowerCase plus an NFD normalize plus a regex replace,
// so a keystroke over a ~1k-rule catalog cannot afford to redo it per option
// (nor redo the query per option). Split the search into a once-per-catalog
// fold and a once-per-keystroke fold, then a pure substring test over the two.
// Module-level, like the CSV and report patterns: the catalog fold runs once
// per option over ~1k rules, and a fresh RegExp per call is a throwaway the
// folded names never keep.
const markRe = /\p{M}/gu;
export const foldForSearch = (value: string): string =>
  value.toLowerCase().normalize('NFD').replace(markRe, '');

// The query half: folded and trimmed once, then matched against every
// pre-folded option. Trimming happens here, so a whitespace-only query is empty
// and matches everything.
export const foldSearchQuery = (query: string): string => foldForSearch(query).trim();

// Substring test between an already-folded option and an already-folded query.
// A linear indexOf per option is bounded: every option is a Kubernetes object
// name, capped at 253 characters by the apiserver whether or not the caller
// ran it through isValidK8sName first.
export const matchesFolded = (foldedOption: string, foldedQuery: string): boolean =>
  foldedQuery.length === 0 || foldedOption.includes(foldedQuery);

// Display order for a user-facing list of names. The locale's collation
// decides where letters sit, so a Turkish list interleaves ı and i, an accented
// name sorts beside its base letter, and an Arabic list orders by abjad.
export const compareForDisplay = (a: string, b: string, locale?: string): number =>
  textCollator(locale).compare(a, b);

// List formatters are as expensive to build as collators, so one per locale.
const listFormatters = new Map<string, Intl.ListFormat>();

// type 'unit' (not 'conjunction'): these lists enumerate peers, so a locale
// that has no list punctuation joins with a space (ja, zh) rather than
// inventing an "and" the sentence never said. Never constructed with the empty
// string, which throws; the empty key is cache-only.
const listFormatter = (locale?: string): Intl.ListFormat => {
  const key = safeLocale(locale) ?? '';
  return intlCached(listFormatters, key, () =>
    new Intl.ListFormat(key || undefined, {
      style: 'long',
      type: 'unit',
    }),
  );
};

// Join a user-facing list of names with the locale's own list punctuation.
// A hardcoded ", " is wrong well beyond Arabic: de and fr want "und"/"et"
// before the last item, ja and zh use no separator at all, and he prefixes the
// final conjunct. The locale tag is validated the same way as the collator.
export const formatList = (items: readonly string[], locale?: string): string =>
  listFormatter(locale).format(items);

// A count of 4 exposes all three separator roles ICU can emit (the literal
// before the first item, between the middle items, and before the last), and a
// count of 2 is the two-item "pair" form, which several locales spell
// differently from the start/end pair ("a und b" vs "a, b, c und d"). Both
// probes are constant-size, so the pattern is derived once per locale and the
// per-call work drops from formatting `count` items to filling count - 1 slots.
const listSeparatorsCache = new Map<string, { start: string; mid: string; end: string; pair: string } | null>;

// The three separator roles plus the two-item pair form for one locale, or
// null when the locale needs no separator at all (ja, zh). intlCached caps the
// map by insertion order; the key is a validated tag, so entries cannot grow
// without bound.
const listSeparatorPattern = (
  locale?: string,
): { start: string; mid: string; end: string; pair: string } | null => {
  const key = safeLocale(locale) ?? '';
  return intlCached(listSeparatorsCache, key, () => {
    const f = listFormatter(locale);
    const literals = (n: number): string[] =>
      f
        .formatToParts(['0', '1', '2', '3'].slice(0, n))
        .filter((p) => p.type === 'literal')
        .map((p) => p.value);
    const pairParts = literals(2);
    // No literal between two items means the locale joins with nothing
    // (ja, zh); every count returns the same empty list.
    if (pairParts.length === 0) {
      return null;
    }
    const four = literals(4);
    return {
      pair: pairParts[0],
      start: four[0] ?? '',
      // A locale with no distinct middle literal (en, sv) exposes two
      // literals for four items; start and end are then the only roles.
      mid: four.length >= 3 ? four[1] : '',
      end: four.length > 0 ? four[four.length - 1] : '',
    };
  });
};

// Same punctuation as formatList, for lists whose items are React nodes (links
// to each check) rather than plain strings. Returns the literals only, in
// order, so a caller can interleave its own elements: item, literal[0], item,
// literal[1], ... A locale that needs no separator (ja, zh) returns an empty
// array and the items simply sit side by side. One fewer element than the item
// count is guaranteed for count >= 2; the count-0 and count-1 cases return
// nothing, since there is no pair to separate.
export const listSeparators = (count: number, locale?: string): string[] => {
  if (count < 2) {
    return [];
  }
  const pattern = listSeparatorPattern(locale);
  if (!pattern) {
    return [];
  }
  if (count === 2) {
    return [pattern.pair];
  }
  // Every interior slot is the same literal, so fill them in one go and set the
  // two ends; a locale needs one fewer separator than there are items.
  const out: string[] = Array.from({ length: count - 1 }, () => pattern.mid);
  out[0] = pattern.start;
  out[count - 2] = pattern.end;
  return out;
};

// The character set Go's strings.TrimSpace trims and strings.Fields splits on
// (unicode.IsSpace plus the Latin-1 specials): the ASCII whitespace controls,
// space, NEL, NBSP, and the Unicode_Space_Separator plus line/paragraph
// separator blocks. Exported because cron.ts splits a schedule's fields on the
// operator's strings.Fields, and the two must not drift apart.
//
// String#trim is NOT this set, and the difference breaks operator parity in
// both directions on the paths that must agree with a Go TrimSpace or
// strings.Fields:
//
//   U+FEFF  JS-only. "node:\uFEFFPASS" is "PASS" to the console and
//           "\uFEFFPASS" (an unknown token, so INCONSISTENT) to the operator.
//   U+0085  operator-only (NEL, C1). "node:\u0085PASS" is "PASS" to the operator
//           and "\u0085PASS" to the console.
//
// Both characters ride in on text pasted from a web page or a word processor,
// and both are invisible, so the mismatch is silent: the console shows one
// status while the operator counts another, or reports a schedule saved that
// leaves the CR Degraded on the next reconcile. Spelled out rather than \s on
// purpose; a future \s that grows a character would reopen the gap silently.
// Keep in lockstep with the Go trim and with normalizeAndParseSchedule's
// strings.Fields in schedule.go.
export const GO_SPACE =
  '\t\n\u000b\f\r\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000';
// Global, so a value padded on both sides loses both runs.
const goSpaceTrimRe = new RegExp(`^[${GO_SPACE}]+|[${GO_SPACE}]+$`, 'gu');

export const trimGoSpace = (value: string): string => value.replace(goSpaceTrimRe, '');

// Length in Unicode code points, the unit a CRD maxLength is expressed in: the
// API server counts runes (utf8.RuneCountInString on the operator's side, the
// same clamp in sanitize.go), not bytes and not UTF-16 code units.
// String#length counts code units, so every astral character counts double: a
// waiver reason of 600 emoji is 600 code points (admitted under MaxLength=1024)
// but 1200 units, and a unit check refuses text the apiserver would have
// accepted. Array.from walks code points, so a surrogate pair counts once and a
// lone surrogate still counts once rather than throwing.
export const codePointLength = (value: string): number => Array.from(value).length;
