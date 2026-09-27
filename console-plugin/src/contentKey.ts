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

// u/z: absent, s<n>:<value>: string of length n, n<value>: number.
export const encodeKeyPart = (value: unknown): string => {
  if (typeof value === 'number') {
    return `n${value}`;
  }
  if (value === undefined) {
    return 'u';
  }
  if (value === null) {
    return 'z';
  }
  if (typeof value === 'string' || typeof value === 'boolean' || typeof value === 'bigint') {
    const s = String(value);
    return `s${s.length}:${s}`;
  }
  // A restored or hand-edited field can be an object. String(object) is
  // "[object Object]" for every object, so two different values would share a
  // key and the memo would keep the previous derivation. JSON is unambiguous
  // for what a CR can hold; a value it cannot carry is tagged by typeof so it
  // cannot forge a string or number part.
  try {
    const s = JSON.stringify(value);
    if (typeof s === 'string') {
      return `j${s.length}:${s}`;
    }
  } catch {
    // Cyclic structure. Fall through to the type tag.
  }
  return `t${typeof value}`;
};

// Same encoding for a whole list, so an element cannot forge the encoding of a
// different list. `['a\0b']` and `['a', 'b']` share a plain join('\0') key, which
// makes the memo serve one derivation for the other. Parts are self-delimiting,
// so they concatenate with no separator of their own.
export const encodeKeyList = (values: readonly unknown[] | undefined): string =>
  (values ?? []).map((v) => encodeKeyPart(v)).join('');
