import { errorMessage, isAlreadyExists } from './errors';

// Deterministic PRNG so fuzz loops are reproducible in CI (no Math.random).
let fuzzSeed = 0x9e3779b9;
const fuzzRand = (): number => {
  fuzzSeed = (Math.imul(fuzzSeed, 1664525) + 1013904223) >>> 0;
  return fuzzSeed / 0x100000000;
};
const randomString = (len: number): string =>
  Array.from({ length: len }, () => String.fromCharCode(Math.floor(fuzzRand() * 0xffff))).join('');

describe('errorMessage', () => {
  it('returns null for empty values', () => {
    expect(errorMessage(null)).toBeNull();
    expect(errorMessage(undefined)).toBeNull();
    expect(errorMessage('')).toBeNull();
  });
  it('handles string and Error', () => {
    expect(errorMessage('boom')).toBe('boom');
    expect(errorMessage(new Error('nope'))).toBe('nope');
  });
  it('handles message-bearing objects (k8s / HttpError shapes)', () => {
    expect(errorMessage({ message: 'forbidden' })).toBe('forbidden');
  });
  // Console HttpError often has message "Conflict" while Status text is on .json.
  it('prefers Kubernetes Status message on .json over generic HTTP phrases', () => {
    const httpErr = Object.assign(new Error('Conflict'), {
      name: 'HttpError',
      code: 409,
      json: { message: 'tailoredprofiles "x" already exists', reason: 'AlreadyExists' },
    });
    expect(errorMessage(httpErr)).toBe('tailoredprofiles "x" already exists');
    expect(
      errorMessage({
        message: 'Conflict',
        json: { message: 'the object has been modified' },
      }),
    ).toBe('the object has been modified');
    // Specific top-level message still wins over json.
    expect(
      errorMessage({
        message: 'custom detail',
        json: { message: 'status body' },
      }),
    ).toBe('custom detail');
  });
  // Every generic HTTP status phrase (not just Conflict) must defer to a real
  // Status detail; dropping any case label would surface the useless phrase.
  it('treats every generic HTTP status phrase as generic (json detail wins)', () => {
    for (const phrase of [
      'Conflict',
      'Forbidden',
      'Bad Request',
      'Not Found',
      'Unauthorized',
      'Too Many Requests',
      'Service Unavailable',
      'Gateway Timeout',
      'Internal Server Error',
    ]) {
      expect(errorMessage({ message: phrase, json: { message: 'real detail' } })).toBe(
        'real detail',
      );
    }
  });
  // Bare objects stringify to "[object Object]", which is useless in Alerts;
  // return null so callers fall back to a translated fail message.
  it('returns null for message-less plain objects (not "[object Object]")', () => {
    expect(errorMessage({})).toBeNull();
    expect(errorMessage({ code: 409 })).toBeNull();
    expect(errorMessage({ message: '' })).toBeNull();
    expect(errorMessage({ message: 42 })).toBeNull();
  });
  it('still stringifies arrays, numbers, and booleans', () => {
    expect(errorMessage(123)).toBe('123');
    expect(errorMessage(true)).toBe('true');
    expect(errorMessage([1, 2])).toBe('1,2');
  });
  it('never throws on hostile values (null-proto, throwing toString, symbol)', () => {
    const hostile: unknown[] = [
      Object.create(null),
      { toString() {
        throw new Error('boom');
      } },
      { get message() {
        throw new Error('getter boom');
      } },
      { message: 42 },
      { message: null },
      [1, 2, 3],
      Symbol('s'),
      123,
      true,
      () => 0,
      new Map(),
    ];
    for (const h of hostile) {
      const out = errorMessage(h);
      expect(out === null || typeof out === 'string').toBe(true);
    }
  });
  it('fuzz: returns string|null and never throws for arbitrary input', () => {
    for (let i = 0; i < 2000; i++) {
      const pool: unknown[] = [
        randomString(i % 40),
        i,
        i % 2 === 0,
        { message: randomString(i % 20) },
        { message: i },
        [randomString(i % 8)],
        i % 7 === 0 ? Object.create(null) : {},
        i % 11 === 0 ? new Error(randomString(i % 16)) : null,
      ];
      const out = errorMessage(pool[i % pool.length]);
      expect(out === null || typeof out === 'string').toBe(true);
    }
  });
});

describe('isAlreadyExists', () => {
  it('detects an AlreadyExists apiserver rejection', () => {
    expect(isAlreadyExists({ reason: 'AlreadyExists' })).toBe(true);
    expect(isAlreadyExists({ code: 409, reason: 'AlreadyExists' })).toBe(true);
    expect(isAlreadyExists({ message: 'tailoredprofiles "x" already exists' })).toBe(true);
    expect(isAlreadyExists({ code: 409, message: 'tailoredprofiles "x" already exists' })).toBe(
      true,
    );
    expect(isAlreadyExists('tailoredprofiles "x" already exists')).toBe(true);
    expect(isAlreadyExists(new Error('tailoredprofiles "x" already exists'))).toBe(true);
    const named = new Error('conflict');
    named.name = 'AlreadyExists';
    expect(isAlreadyExists(named)).toBe(true);
  });
  // Console SDK HttpError: name is "HttpError", reason lives on .json (Status body).
  // Message may be the generic "Conflict" status text while reason is AlreadyExists.
  it('detects console HttpError with Status reason on .json', () => {
    const httpAlready = Object.assign(new Error('Conflict'), {
      name: 'HttpError',
      code: 409,
      json: {
        reason: 'AlreadyExists',
        message: 'tailoredprofiles.compliance.openshift.io "x" already exists',
      },
    });
    expect(isAlreadyExists(httpAlready)).toBe(true);
    const httpConflict = Object.assign(new Error('Conflict'), {
      name: 'HttpError',
      code: 409,
      json: { reason: 'Conflict', message: 'the object has been modified' },
    });
    expect(isAlreadyExists(httpConflict)).toBe(false);
    // Nested json on a plain object (serialized error shape).
    expect(
      isAlreadyExists({
        code: 409,
        message: 'Conflict',
        json: { reason: 'AlreadyExists', message: 'foo already exists' },
      }),
    ).toBe(true);
  });
  it('is false for Conflict (also HTTP 409) and other errors', () => {
    // Bare 409 is ambiguous (AlreadyExists vs Conflict); do not guess.
    expect(isAlreadyExists({ code: 409 })).toBe(false);
    expect(isAlreadyExists({ code: 409, reason: 'Conflict' })).toBe(false);
    expect(isAlreadyExists({ code: 409, message: 'the object has been modified' })).toBe(false);
    expect(isAlreadyExists({ code: 403 })).toBe(false);
    expect(isAlreadyExists(new Error('boom'))).toBe(false);
    expect(isAlreadyExists('forbidden')).toBe(false);
    expect(isAlreadyExists(null)).toBe(false);
  });
  // Untrusted watch/fetch error shapes (partial Status, throwing getters) must
  // never throw; create-retry classification cannot become a second failure mode.
  it('fuzz: never throws; always returns boolean', () => {
    const hostile: unknown[] = [
      null,
      undefined,
      '',
      'already exists',
      'forbidden',
      409,
      true,
      Symbol('s'),
      Object.create(null),
      {
        get reason() {
          throw new Error('getter boom');
        },
      },
      {
        get message() {
          throw new Error('msg boom');
        },
      },
      {
        get json() {
          throw new Error('json boom');
        },
      },
      {
        reason: 'AlreadyExists',
        get message() {
          throw new Error('nested');
        },
      },
      Object.assign(new Error('Conflict'), {
        name: 'HttpError',
        json: {
          get reason() {
            throw new Error('json.reason');
          },
        },
      }),
    ];
    for (let i = 0; i < 2000; i++) {
      const pool: unknown[] = [
        ...hostile,
        randomString(i % 40),
        { reason: i % 3 === 0 ? 'AlreadyExists' : randomString(i % 12) },
        { code: 409, reason: randomString(i % 10), message: randomString(i % 24) },
        { message: i % 5 === 0 ? 'foo already exists' : randomString(i % 20) },
        new Error(i % 4 === 0 ? 'x already exists' : randomString(i % 16)),
        Object.assign(new Error('Conflict'), {
          name: 'HttpError',
          code: 409,
          json: { reason: i % 2 === 0 ? 'AlreadyExists' : 'Conflict', message: randomString(i % 18) },
        }),
        { json: { reason: 'AlreadyExists', message: randomString(i % 10) } },
        [randomString(i % 8)],
      ];
      let out: boolean | undefined;
      expect(() => {
        out = isAlreadyExists(pool[i % pool.length]);
      }).not.toThrow();
      expect(typeof out).toBe('boolean');
    }
  });
});
