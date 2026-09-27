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
  const s = String(value);
  return `s${s.length}:${s}`;
};
