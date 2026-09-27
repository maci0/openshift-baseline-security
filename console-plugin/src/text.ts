// Locale-aware text matching and display ordering for the typeahead option
// lists (profile and rule pickers).
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
export const foldForSearch = (value: string): string =>
  value.toLowerCase().normalize('NFD').replace(/\p{M}/gu, '');

// Case- and accent-insensitive substring match. Folding the whole option
// rather than windowing keeps one code path for every script; a Kubernetes
// name is capped at 253 characters by isValidK8sName, so a linear indexOf per
// option is bounded.
export const matchesSearch = (haystack: string, needle: string): boolean => {
  const query = foldForSearch(needle).trim();
  if (query.length === 0) {
    return true;
  }
  return foldForSearch(haystack).includes(query);
};

// Display order for a user-facing list of names. The locale's collation
// decides where letters sit, so a Turkish list interleaves ı and i, an accented
// name sorts beside its base letter, and an Arabic list orders by abjad.
export const compareForDisplay = (a: string, b: string, locale?: string): number =>
  textCollator(locale).compare(a, b);
