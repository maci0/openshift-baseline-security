// Normalize k8s watch / fetch errors (string | Error | { message }) for Alerts.
// Returns null when there is no user-actionable text so callers can fall back
// to a translated fail message (errorMessage(e) ?? t('…')).
import { isString } from './parse';

// Fields a console SDK HttpError or hand-built rejection may carry. Values are
// untrusted, so every read is narrowed through parse guards before use.
interface RejectionFields {
  message?: unknown;
  json?: unknown;
  reason?: unknown;
  code?: unknown;
}

// One field off a RejectionFields carrier; keeps payload-parser signatures
// tied to the owner shape instead of a bare escape hatch.
type RejectionField = RejectionFields['message'];

// The only object check: anything object-like is treated as a field carrier
// and every consumed field is re-validated, so a lying cast cannot crash us.
const asRejection = (v: unknown): v is RejectionFields =>
  v !== null && typeof v === 'object';

export const errorMessage = (cause: unknown): string | null => {
  if (cause == null || cause === '') {
    return null;
  }
  if (isString(cause)) {
    return cause;
  }
  if (cause instanceof Error) {
    // Prefer Error.message; when empty (or only a generic HTTP status phrase),
    // fall through to console SDK HttpError.json.message (Kubernetes Status).
    if (cause.message && !isGenericHttpStatusMessage(cause.message)) {
      return cause.message;
    }
    // SAFETY: console SDK HttpError instances attach .json; plain Errors from
    // other sources simply read undefined here.
    const fromJson = statusMessageFromJson((cause as RejectionFields).json);
    if (fromJson) {
      return fromJson;
    }
    return cause.message || cause.name || null;
  }
  // A message-bearing object, a null-prototype object, a throwing toString, or a
  // throwing `message` getter must all be tolerated: an error normalizer must
  // never throw. Guard the whole property access + String() fallback.
  try {
    if (asRejection(cause)) {
      if (isString(cause.message) && cause.message && !isGenericHttpStatusMessage(cause.message)) {
        return cause.message;
      }
      const fromJson = statusMessageFromJson(cause.json);
      if (fromJson) {
        return fromJson;
      }
      if (isString(cause.message) && cause.message) {
        return cause.message;
      }
    }
    // Stringifying the residual (numbers, booleans, arrays, exotic objects) is
    // the point of a normalizer; useless forms are filtered below and throwing
    // toString/getters are caught by this try block.
    // eslint-disable-next-line typescript/no-base-to-string -- deliberate unknown-value stringification
    const s = String(cause);
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
const statusMessageFromJson = (json: RejectionField): string | null => {
  if (json == null || !asRejection(json)) {
    return null;
  }
  try {
    const m = json.message;
    return isString(m) && m ? m : null;
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
const reasonIsAlreadyExists = (
  reason: RejectionField,
  message: RejectionField,
): boolean | null => {
  if (reason === 'AlreadyExists') {
    return true;
  }
  if (reason === 'Conflict') {
    return false;
  }
  if (isString(message) && /already exists/i.test(message)) {
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
export const isAlreadyExists = (cause: unknown): boolean => {
  if (isString(cause)) {
    return /already exists/i.test(cause);
  }
  // Property access / regex on untrusted error shapes (console SDK, partial
  // Status, throwing getters) must never throw: a create retry path that
  // classifies errors cannot become a second failure mode.
  try {
    if (cause instanceof Error) {
      if (cause.name === 'AlreadyExists' || /already exists/i.test(cause.message)) {
        return true;
      }
      // SAFETY: console SDK HttpError carries the Kubernetes Status on .json;
      // other Error sources read undefined and skip this branch.
      const errJson = (cause as RejectionFields).json;
      if (errJson != null && asRejection(errJson)) {
        const hit = reasonIsAlreadyExists(errJson.reason, errJson.message);
        if (hit != null) {
          return hit;
        }
      }
      return false;
    }
    if (!asRejection(cause)) {
      return false;
    }
    const top = reasonIsAlreadyExists(cause.reason, cause.message);
    if (top != null) {
      return top;
    }
    const json = cause.json;
    if (json != null && asRejection(json)) {
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
