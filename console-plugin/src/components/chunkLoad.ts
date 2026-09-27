import * as React from 'react';

// Async-chunk load state. Tabs and Overview charts share this so a failed
// import() shows Retry instead of a blank region (stale hashed chunk after
// an upgrade, or a dropped request). `failed` carries the reason: a 404 on a
// stale chunk id, a dropped request, and a webpack parse error are otherwise
// one indistinguishable string, and nothing else logs them.
export type ChunkState<T> =
  | { status: 'loading' }
  | { status: 'ready'; module: T }
  | { status: 'failed'; error: unknown };

// Subscribe to a dynamic-import promise. Returns a cancel function so a
// retry or unmount ignores a late settle. Tested without a DOM.
export const watchChunk = <T>(
  pending: Promise<T>,
  deliver: (next: ChunkState<T>) => void,
): (() => void) => {
  let cancelled = false;
  void pending.then(
    (module: T) => {
      if (!cancelled) {
        deliver({ status: 'ready', module });
      }
    },
    (error: unknown) => {
      if (!cancelled) {
        deliver({ status: 'failed', error });
      }
    },
  );
  return () => {
    cancelled = true;
  };
};

// `attempt` is a retry counter: incrementing it re-invokes `load`. Until the
// new promise settles, render loading by comparing the last settled attempt
// (no setState in the effect body). `load` must be a stable module-level
// function so the effect does not re-fire every render.
export const useChunk = <T>(load: () => Promise<T>, attempt: number): ChunkState<T> => {
  const [settled, setSettled] = React.useState<{ attempt: number; state: ChunkState<T> }>({
    attempt,
    state: { status: 'loading' },
  });
  React.useEffect(() => {
    // watchChunk can only observe a rejection. A load() that throws
    // synchronously must still reach the failed + Retry path instead of
    // escaping the effect body into the console's error boundary.
    let pending: Promise<T>;
    try {
      pending = load();
    } catch (error) {
      setSettled({ attempt, state: { status: 'failed', error } });
      return undefined;
    }
    return watchChunk(pending, (next) => {
      setSettled({ attempt, state: next });
    });
  }, [load, attempt]);
  return settled.attempt === attempt ? settled.state : { status: 'loading' };
};
