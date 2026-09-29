// Unambiguous encoding for the content keys that gate memoized derivations
// (history trend data, waiver expiry scheduling). The values come off a
// ClusterBaseline a user or a restored backup can hand-edit, so a key built by
// pasting fields together with a separator lets a value that contains that
// separator forge another value's key. The memo then skips the recompute and
// the UI keeps showing the previous derivation forever.
//
// Length-prefixing plus a type tag removes the class: no value can produce the
// encoding of a different value, whatever bytes it carries. Encoded parts are
// self-delimiting, so callers concatenate them without a separator of their own.

import {
  isBigInt,
  isBoolean,
  isFunction,
  isNumber,
  isString,
  isSymbol,
} from './parse';
import type { UntrustedValue } from './parse';

// u/z: absent, s<n>:<value>: string of length n, n<value>: number.
export const encodeKeyPart = (value: UntrustedValue): string => {
  if (isNumber(value)) {
    return `n${value}`;
  }
  if (value === undefined) {
    return 'u';
  }
  if (value === null) {
    return 'z';
  }
  if (isString(value) || isBoolean(value) || isBigInt(value)) {
    const s = String(value);
    return `s${s.length}:${s}`;
  }
  // A restored or hand-edited field can be an object. String(object) is
  // "[object Object]" for every object, so two different values would share a
  // key and the memo would keep the previous derivation. JSON is unambiguous
  // for what a CR can hold; a value it cannot carry is tagged by its kind so it
  // cannot forge a string or number part.
  try {
    const s = JSON.stringify(value);
    if (isString(s)) {
      return `j${s.length}:${s}`;
    }
  } catch {
    // Cyclic structure. Fall through to the kind tag.
  }
  return `t${kindOf(value)}`;
};

// The `typeof` spelling of a value JSON.stringify could not carry: `object` for
// a cyclic or toJSON-less graph, `function` / `symbol` for the two kinds the
// encoder never has to read. Every other kind returned above.
const kindOf = (value: UntrustedValue): string => {
  if (isSymbol(value)) {
    return 'symbol';
  }
  return isFunction(value) ? 'function' : 'object';
};

// Same encoding for a whole list, so an element cannot forge the encoding of a
// different list. `['a\0b']` and `['a', 'b']` share a plain join('\0') key, which
// makes the memo serve one derivation for the other. Parts are self-delimiting,
// so they concatenate with no separator of their own.
//
// One accumulation pass, not map(encodeKeyPart).join(''): status.newlyFailed and
// status.fixed are capped at 4096 names each, and encodeKeyList gates the memo
// deps on every Overview, Results, and Remediations render. The map shape builds
// a second array of N encoded strings and hands them all to join; accumulating
// keeps one output string.
export const encodeKeyList = (values: readonly UntrustedValue[] | undefined): string => {
  if (!values) {
    return '';
  }
  let key = '';
  for (const v of values) {
    key += encodeKeyPart(v);
  }
  return key;
};
