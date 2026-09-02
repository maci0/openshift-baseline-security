import { isFiniteNumber, isString, stripControlAndFormat, stripFormatChars } from './parse';

describe('isString / isFiniteNumber', () => {
  it('narrows strings and finite numbers only', () => {
    expect(isString('')).toBeTruthy();
    expect(isString(1)).toBeFalsy();
    expect(isFiniteNumber(0)).toBeTruthy();
    expect(isFiniteNumber(Number.NaN)).toBeFalsy();
    expect(isFiniteNumber(Number.POSITIVE_INFINITY)).toBeFalsy();
  });
});

describe('stripFormatChars', () => {
  it('drops BIDI, zero-width, and BOM but keeps tab/newline and ASCII', () => {
    expect(stripFormatChars('alice')).toBe('alice');
    expect(stripFormatChars('\u200B=cmd')).toBe('=cmd');
    expect(stripFormatChars('safe\u202Eexe.csv')).toBe('safeexe.csv');
    expect(stripFormatChars('a\u200E\u2066b')).toBe('ab');
    expect(stripFormatChars('x\uFEFFy')).toBe('xy');
    expect(stripFormatChars('a\u2060b')).toBe('ab');
    // Tab / CR / LF stay so CSV quoting still sees them.
    expect(stripFormatChars('\t=cmd')).toBe('\t=cmd');
    expect(stripFormatChars('a\nb')).toBe('a\nb');
  });
});

describe('stripControlAndFormat', () => {
  it('drops controls and format characters from identity text', () => {
    expect(stripControlAndFormat('alice')).toBe('alice');
    expect(stripControlAndFormat('alice\u202E')).toBe('alice');
    expect(stripControlAndFormat('admin\u200B')).toBe('admin');
    expect(stripControlAndFormat('a\tb')).toBe('ab');
    expect(stripControlAndFormat('a\0b')).toBe('ab');
    expect(stripControlAndFormat('')).toBe('');
  });
});
