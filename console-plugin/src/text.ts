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

import { safeLocale } from './dates';

// Collators are expensive to build and a ~1k-rule catalog is sorted on every
// render, so keep one per locale tag. A module-level Map is bounded by the
// locales a console session can offer (tens), not by input.
const collators = new Map<string, Intl.Collator>();

// Construct with the validated tag, never with the empty string: new
// Intl.Collator('') throws RangeError, while undefined means "runtime default".
// The empty key is cache-only.
export const textCollator = (locale?: string): Intl.Collator => {
  const tag = safeLocale(locale);
  const key = tag ?? '';
  let collator = collators.get(key);
  if (!collator) {
    // numeric: true orders rule_2 before rule_10 the way a reader expects,
    // rather than by byte value where '_' (0x5f) sorts above every digit.
    collator = new Intl.Collator(tag, { usage: 'sort', numeric: true });
    collators.set(key, collator);
  }
  return collator;
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
export const foldForSearch = (value: string): string =>
  value.toLowerCase().normalize('NFD').replace(/\p{M}/gu, '');

// The query half: folded and trimmed once, then matched against every
// pre-folded option. Trimming happens here, so a whitespace-only query is empty
// and matches everything.
export const foldSearchQuery = (query: string): string => foldForSearch(query).trim();

// Substring test between an already-folded option and an already-folded query.
// A linear indexOf per option is bounded: a Kubernetes name is capped at 253
// characters by isValidK8sName.
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
  let formatter = listFormatters.get(key);
  if (!formatter) {
    formatter = new Intl.ListFormat(key || undefined, {
      style: 'long',
      type: 'unit',
    });
    listFormatters.set(key, formatter);
  }
  return formatter;
};

// Join a user-facing list of names with the locale's own list punctuation.
// A hardcoded ", " is wrong well beyond Arabic: de and fr want "und"/"et"
// before the last item, ja and zh use no separator at all, and he prefixes the
// final conjunct. The locale tag is validated the same way as the collator.
export const formatList = (items: readonly string[], locale?: string): string =>
  listFormatter(locale).format(items);

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
  const parts = listFormatter(locale).formatToParts(
    Array.from({ length: count }, (_, i) => String(i)),
  );
  return parts.filter((p) => p.type === 'literal').map((p) => p.value);
};

// Length in Unicode code points, the unit a CRD maxLength is expressed in: the
// API server counts runes (utf8.RuneCountInString on the operator's side, the
// same clamp in sanitize.go), not bytes and not UTF-16 code units.
// String#length counts code units, so every astral character counts double: a
// waiver reason of 600 emoji is 600 code points (admitted under MaxLength=1024)
// but 1200 units, and a unit check refuses text the apiserver would have
// accepted. Array.from walks code points, so a surrogate pair counts once and a
// lone surrogate still counts once rather than throwing.
export const codePointLength = (value: string): number => Array.from(value).length;
