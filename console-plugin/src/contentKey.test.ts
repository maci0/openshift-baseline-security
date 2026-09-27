import { encodeKeyPart } from './contentKey';

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
