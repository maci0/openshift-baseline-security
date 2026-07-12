// Normalize k8s watch / fetch errors (string | Error | { message }) for Alerts.
// Returns null when there is no user-actionable text so callers can fall back
// to a translated fail message (errorMessage(e) ?? t('…')).
export const errorMessage = (err: unknown): string | null => {
  if (err == null || err === '') {
    return null;
  }
  if (typeof err === 'string') {
    return err;
  }
  if (err instanceof Error) {
    // Prefer Error.message; when empty (or only a generic HTTP status phrase),
    // fall through to console SDK HttpError.json.message (Kubernetes Status).
    if (err.message && !isGenericHttpStatusMessage(err.message)) {
      return err.message;
    }
    const fromJson = statusMessageFromJson((err as { json?: unknown }).json);
    if (fromJson) {
      return fromJson;
    }
    return err.message || err.name || null;
  }
  // A message-bearing object, a null-prototype object, a throwing toString, or a
  // throwing `message` getter must all be tolerated: an error normalizer must
  // never throw. Guard the whole property access + String() fallback.
  try {
    if (typeof err === 'object') {
      const o = err as { message?: unknown; json?: unknown };
      if (typeof o.message === 'string' && o.message && !isGenericHttpStatusMessage(o.message)) {
        return o.message;
      }
      const fromJson = statusMessageFromJson(o.json);
      if (fromJson) {
        return fromJson;
      }
      if (typeof o.message === 'string' && o.message) {
        return o.message;
      }
    }
    const s = String(err);
    // Default Object.prototype.toString is useless in Alerts; treat as absent so
    // UI copy stays translated. Arrays / numbers / booleans still stringify.
    if (!s || s === '[object Object]') {
      return null;
    }
    return s;
  } catch {
    // Unserializable err: let callers fall back to their translated fail message
    // instead of a hardcoded English string.
    return null;
  }
};

// Kubernetes Status.message from a console SDK HttpError.json body, if present.
const statusMessageFromJson = (json: unknown): string | null => {
  if (json == null || typeof json !== 'object') {
    return null;
  }
  try {
    const m = (json as { message?: unknown }).message;
    return typeof m === 'string' && m ? m : null;
  } catch {
    return null;
  }
};

// Phrases from HttpError.messages / statusText that are not actionable alone
// when a Status body is also available (prefer json.message).
const isGenericHttpStatusMessage = (m: string): boolean => {
  switch (m) {
    case 'Conflict':
    case 'Bad Request':
    case 'Forbidden':
    case 'Not Found':
    case 'Unauthorized':
    case 'Internal Server Error':
    case 'Too Many Requests':
    case 'Service Unavailable':
    case 'Gateway Timeout':
      return true;
    default:
      return false;
  }
};

// True when value looks like a Kubernetes Status reason for AlreadyExists.
// Shared by flat Status objects and nested HttpError.json bodies.
const reasonIsAlreadyExists = (reason: unknown, message: unknown): boolean | null => {
  if (reason === 'AlreadyExists') {
    return true;
  }
  if (reason === 'Conflict') {
    return false;
  }
  if (typeof message === 'string' && /already exists/i.test(message)) {
    return true;
  }
  return null;
};

// True for an apiserver AlreadyExists rejection, so a create can be retried
// idempotently after a later step failed. Tolerates Status objects, Error
// instances, console SDK HttpError (reason on .json), and plain strings.
//
// Do not treat bare HTTP 409 as AlreadyExists: Conflict (optimistic concurrency
// / resourceVersion mismatch on patch) is also 409. Prefer reason, then
// message text; bare code alone is ambiguous and returns false.
export const isAlreadyExists = (e: unknown): boolean => {
  if (typeof e === 'string') {
    return /already exists/i.test(e);
  }
  // Property access / regex on untrusted error shapes (console SDK, partial
  // Status, throwing getters) must never throw: a create retry path that
  // classifies errors cannot become a second failure mode.
  try {
    if (e instanceof Error) {
      if (e.name === 'AlreadyExists' || /already exists/i.test(e.message)) {
        return true;
      }
      // OpenShift console HttpError: Kubernetes Status is on .json, not top-level.
      // message may be the generic "Conflict" status text while reason is AlreadyExists.
      const json = (e as { json?: { reason?: unknown; message?: unknown } }).json;
      if (json != null && typeof json === 'object') {
        const hit = reasonIsAlreadyExists(json.reason, json.message);
        if (hit != null) {
          return hit;
        }
      }
      return false;
    }
    const o = e as {
      code?: number;
      reason?: string;
      message?: string;
      json?: { reason?: unknown; message?: unknown };
    } | null;
    if (o == null || typeof o !== 'object') {
      return false;
    }
    const top = reasonIsAlreadyExists(o.reason, o.message);
    if (top != null) {
      return top;
    }
    const json = o.json;
    if (json != null && typeof json === 'object') {
      const nested = reasonIsAlreadyExists(json.reason, json.message);
      if (nested != null) {
        return nested;
      }
    }
    return false;
  } catch {
    // Throwing getters / hostile proxies: fail closed (not AlreadyExists).
    return false;
  }
};
