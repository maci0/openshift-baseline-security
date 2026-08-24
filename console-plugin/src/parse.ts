// Boundary guards for values that k8s payloads and browser APIs deliver
// without runtime validation. Every representation check lives here so call
// sites branch on domain values instead of raw typeof expressions.

export const isString = (v: unknown): v is string => typeof v === 'string';

// Finite only: CR numerics feed Math.min/max/format pipelines where NaN or
// Infinity must fall back, not propagate.
export const isFiniteNumber = (v: unknown): v is number =>
  typeof v === 'number' && Number.isFinite(v);
