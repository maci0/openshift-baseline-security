import { buildReportHtml } from './report';
import { ClusterBaseline, ComplianceCheckResult, Waiver } from './models';

// buildReportHtml renders a self-contained HTML report from ClusterBaseline
// status and ComplianceCheckResult CRs. Every one of those fields is untrusted:
// a hand-edited CR (or a compromised in-cluster actor) controls waiver reasons,
// rule names, descriptions, and severity labels. The builder promises to
// HTML-escape all of it and never throw on tampered/mistyped values. Nothing
// pins that promise. This is a fuzz sweep: inject markup-breakout payloads into
// every untrusted string and assert (1) no injected tag survives unescaped
// (XSS), and (2) the builder never throws on malformed input.

// Markup/JS breakout corpus. Each opens a tag that does NOT appear in the report
// chrome (chrome uses html/head/meta/style/body/h*/table/tr/td...), so its
// literal presence in the output can only mean an untrusted field leaked
// unescaped. Also mix in bare special chars, control bytes, and huge strings.
const XSS = [
  '<script>alert(1)</script>',
  '<img src=x onerror=alert(1)>',
  '<svg/onload=alert(1)>',
  '<iframe src=javascript:alert(1)>',
  '"><script>alert(1)</script>',
  "'><img src=x onerror=alert(1)>",
  '</td></tr><script>alert(1)</script>',
  '&lt;script&gt;', // already-escaped: must not be double-decoded
  '<object data=x>',
  '<marquee>',
  '&amp;<b>',
  '\0<script>',
  '"onmouseover="alert(1)',
  ' <script>', // line separator
  '<'.repeat(500) + 'script',
  '',
  ' ',
];
// Tag-open markers that must never appear literally in the output.
const FORBIDDEN = ['<script', '<img', '<svg', '<iframe', '<object', '<marquee', '<b>'];

// Deterministic PRNG (mulberry32) so failures reproduce without a fixed corpus
// file and CI stays stable (no Math.random).
const rng = (seed: number) => () => {
  seed |= 0;
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};

const pick = (rand: () => number): string => XSS[Math.floor(rand() * XSS.length)];

// Build a baseline whose every untrusted string field carries an XSS payload.
const hostileBaseline = (rand: () => number): ClusterBaseline => {
  const waivers: Waiver[] = Array.from({ length: 1 + Math.floor(rand() * 4) }, () => ({
    name: pick(rand),
    reason: pick(rand),
    requestedBy: pick(rand),
    approvedBy: pick(rand),
    expiresAt: pick(rand),
    reviewBy: pick(rand),
  }));
  return {
    metadata: { name: pick(rand) },
    spec: {
      profiles: [pick(rand)],
      tailoredProfiles: [pick(rand)],
      waivers,
    },
    status: {
      // Coercion path: pass/fail typed number but a tampered CR can carry markup.
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      score: rand() < 0.5 ? (pick(rand) as any) : Math.floor(rand() * 200) - 50,
      lastScanTime: pick(rand),
      profiles: [
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        { key: pick(rand), pass: pick(rand) as any, fail: pick(rand) as any } as any,
      ],
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      tailoredProfiles: [{ name: pick(rand), pass: pick(rand) as any } as any],
    },
  };
};

const hostileResults = (rand: () => number): ComplianceCheckResult[] =>
  Array.from({ length: Math.floor(rand() * 5) }, () => ({
    metadata: {
      name: pick(rand),
      namespace: 'openshift-compliance',
      // Labels drive isOwnedByBaseline/checkProfileLabel; feed markup + a real key.
      labels: {
        'compliance.openshift.io/profile': pick(rand),
        'compliance.openshift.io/check-severity': pick(rand),
      },
    },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    status: (rand() < 0.5 ? 'FAIL' : pick(rand)) as any,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    severity: pick(rand) as any,
    description: pick(rand),
  }));

describe('buildReportHtml fuzz sweep', () => {
  const NOW = new Date('2026-07-13T00:00:00Z');

  it('never emits an unescaped injected tag and never throws', () => {
    for (let seed = 0; seed < 400; seed++) {
      const rand = rng(seed);
      const baseline = hostileBaseline(rand);
      const results = hostileResults(rand);

      let html = '';
      expect(() => {
        html = buildReportHtml(baseline, results, NOW);
      }).not.toThrow();

      // Empty / whitespace-only output would pass the XSS checks below with false
      // confidence; require a real document shell and score chrome.
      expect(html.toLowerCase()).toContain('<!doctype html');
      expect(html.toLowerCase()).toContain('<html');
      expect(html.length).toBeGreaterThan(200);

      for (const marker of FORBIDDEN) {
        // Case-insensitive: an unescaped payload would leak the tag verbatim.
        expect(html.toLowerCase()).not.toContain(marker);
      }
    }
  });

  it('tolerates empty / minimal baselines', () => {
    const empty: ClusterBaseline = {
      metadata: { name: '' },
      spec: { profiles: [] },
    };
    const html = buildReportHtml(empty, [], NOW);
    expect(html.toLowerCase()).toContain('<!doctype html');
    expect(html.toLowerCase()).toContain('<html');
    expect(() => buildReportHtml(empty)).not.toThrow();
  });
});
