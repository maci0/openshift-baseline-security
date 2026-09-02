import { downloadBlob } from './download';
import { randomString } from './testing/fuzz';
import { isString } from './parse';

describe('downloadBlob', () => {
  type AnchorStub = {
    href: string;
    download: string;
    rel: string;
    style: { display: string };
    click: jest.Mock;
    remove: jest.Mock;
  };

  // The slice of the browser globals downloadBlob touches, swapped in for each
  // test and restored afterwards. Blob is only preserved, never invoked.
  type StubbedGlobals = {
    URL: { createObjectURL: (blob: Blob) => string; revokeObjectURL: (url: string) => void };
    document: {
      createElement: (tagName: string) => AnchorStub;
      body: { appendChild: (child: AnchorStub) => void };
    };
    window: { setTimeout: (fn: () => void) => number };
    Blob?: unknown;
  };

  const installDom = () => {
    const anchor: AnchorStub = {
      href: '',
      download: '',
      rel: '',
      style: { display: '' },
      click: jest.fn(),
      remove: jest.fn(),
    };
    const createObjectURL = jest.fn(() => 'blob:mock-url');
    const revokeObjectURL = jest.fn();
    // SAFETY: jest runs this file under node, so these browser globals are
    // absent from globalThis or carry incompatible native node shapes; the
    // test installs stubs matching StubbedGlobals and restores the originals.
    const host = globalThis as {
      URL?: unknown;
      document?: unknown;
      window?: unknown;
      Blob?: unknown;
    };
    // SAFETY: host above only proves which keys may exist; every read and
    // write inside this helper follows the concrete StubbedGlobals shapes.
    const g = host as StubbedGlobals;
    const prev = {
      URL: g.URL,
      document: g.document,
      window: g.window,
      Blob: g.Blob,
    };
    g.URL = { createObjectURL, revokeObjectURL };
    g.document = {
      createElement: () => anchor,
      body: { appendChild: jest.fn() },
    };
    // Run revoke callbacks inline so the test can assert without waiting.
    g.window = { setTimeout: (fn: () => void) => { fn(); return 0; } };
    if (g.Blob === undefined) {
      g.Blob = class {
        // Minimal Blob stand-in for node; production uses the real DOM Blob.
        constructor(public parts: unknown[]) {}
      };
    }
    return {
      anchor,
      createObjectURL,
      revokeObjectURL,
      restore: () => {
        g.URL = prev.URL;
        g.document = prev.document;
        g.window = prev.window;
        g.Blob = prev.Blob;
      },
    };
  };

  it('sanitizes path traversal and control chars in the download filename', () => {
    const dom = installDom();
    try {
      downloadBlob(new Blob(['x']), '../../../etc/passwd');
      expect(dom.anchor.download).toBe('______etc_passwd');
      expect(dom.anchor.download).not.toContain('/');
      expect(dom.anchor.download).not.toContain('..');
      // Defense in depth when a browser ignores the download attribute.
      expect(dom.anchor.rel).toBe('noopener noreferrer');
      // Exactly once, spelled as two bounds: the preset bans both
      // toHaveBeenCalledTimes(1) and toHaveBeenCalledOnce().
      expect(dom.createObjectURL).toHaveBeenCalled();
      expect(dom.createObjectURL).not.toHaveBeenCalledTimes(2);
      expect(dom.anchor.click).toHaveBeenCalled();
      expect(dom.anchor.click).not.toHaveBeenCalledTimes(2);
      expect(dom.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');
    } finally {
      dom.restore();
    }
  });

  it('falls back to "download" for empty/dot-only names and keeps safe names', () => {
    const cases: [string, string][] = [
      ['', 'download'],
      ['.', '_'],
      ['..', '_'],
      ['ok.csv', 'ok.csv'],
      ['a/b\\c:d', 'a_b_c_d'],
      ['report.html', 'report.html'],
      // BIDI override must not spoof extension direction (e.g. exe.gpj\\u202E).
      ['safe\u202Eexe.csv', 'safe_exe.csv'],
      ['a\u200E\u2066b.csv', 'a__b.csv'],
      // Zero-width / BOM must not hide path segments or extension spoofing.
      ['a\u200Bb.csv', 'a_b.csv'],
      ['x\uFEFFy.csv', 'x_y.csv'],
      // Word joiner (Cf) must not hide an extension swap.
      ['ok\u2060.exe.csv', 'ok_.exe.csv'],
    ];
    for (const [input, want] of cases) {
      const dom = installDom();
      try {
        downloadBlob(new Blob(['x']), input);
        expect(dom.anchor.download).toBe(want);
      } finally {
        dom.restore();
      }
    }
  });

  it('caps oversized filenames at 200 characters', () => {
    const dom = installDom();
    try {
      downloadBlob(new Blob(['x']), `${'a'.repeat(300)}.csv`);
      expect(dom.anchor.download.length).toBe(200);
      expect(dom.anchor.download.startsWith('aaa')).toBeTruthy();
    } finally {
      dom.restore();
    }
  });

  it('revokes the object URL even when click throws', () => {
    const dom = installDom();
    dom.anchor.click.mockImplementation(() => {
      throw new Error('click failed');
    });
    try {
      expect(() => downloadBlob(new Blob(['x']), 'ok.csv')).toThrow('click failed');
      expect(dom.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');
    } finally {
      dom.restore();
    }
  });

  // Filename is sometimes CR-derived (export name). Sanitization must never throw
  // and must strip path/control/BIDI noise that could bias the browser save path.
  it('fuzz: download filename is non-empty, capped, and free of path/control/BIDI', () => {
    const seeds = [
      '',
      '.',
      '..',
      '../../../etc/passwd',
      'a/b\\c:d',
      'safe\u202Eexe.csv',
      'a\u200E\u2066b.csv',
      'a'.repeat(300),
      'report\0.csv',
      'ok.csv',
    ];
    for (let i = 0; i < 500; i++) {
      const name =
        i < seeds.length
          ? seeds[i]
          : randomString(i % 80) + (i % 7 === 0 ? '/../' : '') + (i % 5 === 0 ? '\u202E' : '');
      const dom = installDom();
      try {
        expect(() => downloadBlob(new Blob(['x']), name)).not.toThrow();
        const d = dom.anchor.download;
        expect(isString(d)).toBeTruthy();
        expect(d.length).toBeGreaterThan(0);
        expect(d.length).toBeLessThanOrEqual(200);
        expect(d).not.toContain('/');
        expect(d).not.toContain('\\');
        expect(d).not.toContain('..');
        expect(d).not.toMatch(/\p{Cc}/u);
        expect(d).not.toMatch(/\p{Cf}/u);
        expect(dom.anchor.rel).toBe('noopener noreferrer');
        expect(dom.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');
      } finally {
        dom.restore();
      }
    }
  });
});
