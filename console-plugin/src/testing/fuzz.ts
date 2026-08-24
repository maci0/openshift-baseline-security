// Deterministic PRNG helpers so fuzz loops are reproducible in CI (no
// Math.random). Jest isolates module state per test file, so each file drawing
// from the default stream starts at the same seed and repeats its sequence.

let fuzzSeed = 0x9e3779b9;

export const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};

export const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

// mulberry32: 32-bit seeded factory for structured generators that thread a
// rand parameter, so one test file can run several independent streams.
export const mulberry32 =
  (seed: number) =>
  (): number => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
