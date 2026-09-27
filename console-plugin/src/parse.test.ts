import {
  isFiniteNumber,
  isString,
  stripControlAndFormat,
  stripExportControls,
  stripFormatChars,
  stripInvisibleText,
} from './parse';

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

describe('stripInvisibleText', () => {
  it('drops the hidden characters from prose', () => {
    expect(stripInvisibleText('accepted risk')).toBe('accepted risk');
    expect(stripInvisibleText('safe\u202Eexe.csv')).toBe('safeexe.csv');
    expect(stripInvisibleText('\u200B=cmd')).toBe('=cmd');
    expect(stripInvisibleText('x\uFEFFy')).toBe('xy');
    expect(stripInvisibleText('a\u2060b')).toBe('ab');
    expect(stripInvisibleText('a\tb\0c')).toBe('abc');
  });

  it('keeps ZWJ and ZWNJ, which are script content', () => {
    // Family emoji: dropping the ZWJ turns one glyph into three.
    expect(stripInvisibleText('\uD83D\uDC68\u200D\uD83D\uDC69\u200D\uD83D\uDC67')).toBe(
      '\uD83D\uDC68\u200D\uD83D\uDC69\u200D\uD83D\uDC67',
    );
    // Persian compound (koshida-free ZWNJ spelling) and a Devanagari conjunct.
    expect(stripInvisibleText('m\u200Cmi')).toBe('m\u200Cmi');
    expect(stripInvisibleText('k\u200Dsh')).toBe('k\u200Dsh');
    // The identity strip still refuses them: an identity has no use for a joiner.
    expect(stripControlAndFormat('\u200Dadmin')).toBe('admin');
  });
});

describe('stripExportControls', () => {
  it('drops the controls a spreadsheet trims but keeps the CSV delimiters', () => {
    expect(stripExportControls('\x01=cmd')).toBe('=cmd');
    expect(stripExportControls('\x1f|dde')).toBe('|dde');
    expect(stripExportControls('a\0b\x7fc')).toBe('abc');
    // Tab/CR/LF are RFC 4180 delimiters: quoting still has to see them.
    expect(stripExportControls('a\tb')).toBe('a\tb');
    expect(stripExportControls('a\r\nb')).toBe('a\r\nb');
    expect(stripExportControls('a\nb')).toBe('a\nb');
    // Format characters are a separate concern; this must not double as esc().
    expect(stripExportControls('a\u202Eb')).toBe('a\u202Eb');
  });
});
