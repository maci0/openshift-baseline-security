import { encodeKeyList, encodeKeyPart } from './contentKey';
import { mulberry32, randomString } from './testing/fuzz';

const NUL = String.fromCharCode(0);
const SOH = String.fromCharCode(1);

describe('encodeKeyPart', () => {
  it('is stable for the same value', () => {
    expect(encodeKeyPart('rule_a')).toBe(encodeKeyPart('rule_a'));
    expect(encodeKeyPart(80)).toBe(encodeKeyPart(80));
  });

  it('separates a number from its decimal string', () => {
    expect(encodeKeyPart(1)).not.toBe(encodeKeyPart('1'));
  });

  it('separates a zero score from a missing one', () => {
    expect(encodeKeyPart(0)).not.toBe(encodeKeyPart(undefined));
    expect(encodeKeyPart(0)).not.toBe(encodeKeyPart(''));
    expect(encodeKeyPart(undefined)).not.toBe(encodeKeyPart(null));
  });

  it('length-prefixes so a value cannot forge another part boundary', () => {
    // Concatenated without a separator, so 'a' + '1b' must not read as 'a1' + 'b'.
    expect(encodeKeyPart('a') + encodeKeyPart('1b')).not.toBe(
      encodeKeyPart('a1') + encodeKeyPart('b'),
    );
  });

  it('encodes separator bytes as ordinary content', () => {
    expect(encodeKeyPart(`a${NUL}b${SOH}c`)).toContain(`a${NUL}b${SOH}c`);
  });
});

describe('encodeKeyList', () => {
  it('is stable for the same list', () => {
    expect(encodeKeyList(['cis', 'cis-node'])).toBe(encodeKeyList(['cis', 'cis-node']));
  });

  it('does not let an element forge a different list', () => {
    // The defect a plain join('\0') has: these two produce the same string.
    expect(encodeKeyList([`a${NUL}b`])).not.toBe(encodeKeyList(['a', 'b']));
    expect(encodeKeyList(['a', 'bc'])).not.toBe(encodeKeyList(['ab', 'c']));
  });

  it('keeps an absent list distinct from an empty one', () => {
    expect(encodeKeyList(undefined)).toBe(encodeKeyList([]));
    expect(encodeKeyList(['a'])).not.toBe(encodeKeyList(['a', '']));
  });
});

// Injective-encoding fuzz. Every hand-picked case above asserts one collision
// that matters; none of them prove the general claim the module makes, that no
// value can encode to another value's key. The keys gate memoized derivations
// (trend points, waiver expiry), so a collision is not a wrong string: the UI
// keeps serving the previous derivation for a history that changed.
//
// The oracle is the inverse of the encoding's promise, so it holds for every
// input rather than for a sample of them: equal keys imply values that print
// the same and share a type. Anything wider is a forge.
const KEY_LEAVES: unknown[] = [
  undefined,
  null,
  '',
  ' ',
  '0',
  '0.0',
  '1',
  '-1',
  '1e3',
  'NaN',
  'Infinity',
  '-Infinity',
  'null',
  'undefined',
  0,
  -0,
  1,
  -1,
  1.5,
  Number.MAX_SAFE_INTEGER,
  Number.EPSILON,
  NaN,
  Infinity,
  -Infinity,
];

const keyLeaf = (next: () => number): unknown => {
  if (next() < 0.5) {
    return KEY_LEAVES[Math.floor(next() * KEY_LEAVES.length)];
  }
  // Separator bytes, encoding-looking fragments, and boundary-adjacent strings:
  // the bytes a length prefix is supposed to defuse.
  const frag = ['n', 's', 'u', 'z', 'n1', 's1:', 's0:', ':', String.fromCharCode(0), String.fromCharCode(1)];
  return frag[Math.floor(next() * frag.length)] + randomString(Math.floor(next() * 6));
};

const keyList = (next: () => number): unknown[] =>
  Array.from({ length: Math.floor(next() * 4) }, () => keyLeaf(next));

describe('content key injectivity fuzz', () => {
  it('equal keys imply values that print the same and share a type', () => {
    for (let seed = 1; seed <= 5000; seed++) {
      const next = mulberry32(seed);
      const a = keyLeaf(next);
      const b = keyLeaf(next);
      if (encodeKeyPart(a) === encodeKeyPart(b)) {
        if (typeof a !== typeof b || String(a) !== String(b)) {
          throw new Error(`seed ${seed}: ${JSON.stringify(a)} and ${JSON.stringify(b)} share a key`);
        }
      }
    }
  });

  it('equal list keys imply the same length and the same elements', () => {
    for (let seed = 1; seed <= 5000; seed++) {
      const next = mulberry32(seed);
      const a = keyList(next);
      const b = keyList(next);
      if (encodeKeyList(a) !== encodeKeyList(b)) {
        continue;
      }
      if (a.length !== b.length) {
        throw new Error(`seed ${seed}: ${JSON.stringify(a)} and ${JSON.stringify(b)} share a key`);
      }
      for (let i = 0; i < a.length; i++) {
        if (typeof a[i] !== typeof b[i] || String(a[i]) !== String(b[i])) {
          throw new Error(
            `seed ${seed} element ${i}: ${JSON.stringify(a[i])} and ${JSON.stringify(b[i])} share a key`,
          );
        }
      }
    }
  });
});
