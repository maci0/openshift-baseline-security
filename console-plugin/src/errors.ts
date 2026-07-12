// Normalize k8s watch / fetch errors (string | Error | { message }) for Alerts.
export const errorMessage = (err: unknown): string | null => {
  if (err == null || err === '') {
    return null;
  }
  if (typeof err === 'string') {
    return err;
  }
  if (err instanceof Error) {
    return err.message || err.name;
  }
  // A message-bearing object, a null-prototype object, a throwing toString, or a
  // throwing `message` getter must all be tolerated: an error normalizer must
  // never throw. Guard the whole property access + String() fallback.
  try {
    if (typeof err === 'object' && 'message' in err) {
      const m = (err as { message: unknown }).message;
      if (typeof m === 'string' && m) {
        return m;
      }
    }
    return String(err);
  } catch {
    // Unserializable err: let callers fall back to their translated fail message
    // (errorMessage(e) ?? t('…')) instead of a hardcoded English string.
    return null;
  }
};

// True for an apiserver AlreadyExists rejection, so a create can be retried
// idempotently after a later step failed. Tolerates Status objects, Error
// instances, and plain strings (SDK shapes vary by call path).
//
// Do not treat bare HTTP 409 as AlreadyExists: Conflict (optimistic concurrency
// / resourceVersion mismatch on patch) is also 409. Prefer reason, then
// message text; bare code alone is ambiguous and returns false.
export const isAlreadyExists = (e: unknown): boolean => {
  if (typeof e === 'string') {
    return /already exists/i.test(e);
  }
  if (e instanceof Error) {
    return e.name === 'AlreadyExists' || /already exists/i.test(e.message);
  }
  const o = e as { code?: number; reason?: string; message?: string } | null;
  if (o == null || typeof o !== 'object') {
    return false;
  }
  if (o.reason === 'AlreadyExists') {
    return true;
  }
  if (o.reason === 'Conflict') {
    return false;
  }
  return typeof o.message === 'string' && /already exists/i.test(o.message);
};
