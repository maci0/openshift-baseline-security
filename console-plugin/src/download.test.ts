import { downloadBlob } from './download';

// Deterministic PRNG so fuzz loops are reproducible in CI (no Math.random).
let fuzzSeed = 0x9e3779b9;
const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};
const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

describe('downloadBlob', () => {
  type AnchorStub = {
    href: string;
    download: string;
    rel: string;
    style: { display: string };
    click: jest.Mock;
    remove: jest.Mock;
  };

  const installDom = (): {
    anchor: AnchorStub;
    createObjectURL: jest.Mock;
    revokeObjectURL: jest.Mock;
    restore: () => void;
  } => {
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
    const g = globalThis as Record<string, unknown>;
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
    if (typeof g.Blob === 'undefined') {
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
      expect(dom.createObjectURL).toHaveBeenCalledTimes(1);
      expect(dom.anchor.click).toHaveBeenCalledTimes(1);
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
        expect(typeof d).toBe('string');
        expect(d.length).toBeGreaterThan(0);
        expect(d.length).toBeLessThanOrEqual(200);
        expect(d).not.toContain('/');
        expect(d).not.toContain('\\');
        expect(d).not.toContain('..');
        expect(d).not.toMatch(/[\0-\x1f\x7f]/);
        expect(d).not.toMatch(/[\u200B-\u200D\u200E\u200F\u202A-\u202E\u2066-\u2069\uFEFF]/);
        expect(dom.anchor.rel).toBe('noopener noreferrer');
        expect(dom.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url');
      } finally {
        dom.restore();
      }
    }
  });
});
