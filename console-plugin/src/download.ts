// Strip path separators, control characters, relative segments, and bidirectional
// overrides so a hostile filename cannot bias the browser save path or spoof
// extensions via RTL (defense in depth; callers use fixed names today).
// Leading dots become underscores (no hidden-file names). Cap length so a huge
// CR-derived name cannot create an oversized Content-Disposition path.
const safeDownloadName = (filename: string): string => {
  const cleaned = filename
    // Path separators, C0/C1 controls, and format characters (BIDI, zero-width,
    // BOM, word joiner) that can spoof extensions or hide path segments.
    .replace(/[/\\:\p{Cc}\p{Cf}]/gu, '_')
    .replace(/\.\./g, '_')
    .replace(/^\.+/, '_')
    .trim()
    .slice(0, 200);
  return cleaned || 'download';
};

// Trigger a browser download of an in-memory blob via a detached anchor.
// Revoke on the next tick so the click has consumed the object URL first.
// Always schedule revoke (try/finally) so a DOM throw cannot leak the blob URL.
export const downloadBlob = (blob: Blob, filename: string): void => {
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement('a');
    a.href = url;
    a.download = safeDownloadName(filename);
    // No navigation target, but set rel in case a browser ignores download.
    a.rel = 'noopener noreferrer';
    a.style.display = 'none';
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  }
};

// Grace so a newly opened tab can fetch the blob before we revoke it. Longer
// than a typical HTML report load; shorter than a session, so a click storm
// cannot pin reports for the life of the console tab.
export const BLOB_TAB_REVOKE_MS = 60_000;

// Handle for a blob URL opened as a document. `opened` is false when the popup
// was blocked (caller should fall back to downloadBlob). `dispose` is
// idempotent: revoke now and cancel the grace timer.
export type BlobTabHandle = {
  opened: boolean;
  dispose: () => void;
};

// Open a blob as a new tab without leaking the object URL: revoke immediately
// when the popup is blocked or window.open throws, and after BLOB_TAB_REVOKE_MS
// when the tab actually opens. Dropping the handle without dispose() is fine:
// the grace timer still revokes. dispose() cancels that timer and revokes now.
export const openBlobInTab = (blob: Blob): BlobTabHandle => {
  const url = URL.createObjectURL(blob);
  // Browser setTimeout returns a number; @types/node must not widen this to Timeout.
  let timer: number | undefined;
  let disposed = false;
  const dispose = (): void => {
    if (disposed) {
      return;
    }
    disposed = true;
    if (timer !== undefined) {
      window.clearTimeout(timer);
    }
    URL.revokeObjectURL(url);
  };
  try {
    // Do NOT pass noopener/noreferrer in the feature string: per the HTML spec
    // that forces window.open to return null even when the tab opens, which
    // would make every caller take the "popup was blocked" path. Open plainly
    // so a real block is the only null, then drop opener manually.
    const w = window.open(url, '_blank');
    if (!w) {
      dispose();
      return { opened: false, dispose };
    }
    w.opener = null;
    timer = window.setTimeout(dispose, BLOB_TAB_REVOKE_MS);
    return { opened: true, dispose };
  } catch (err) {
    dispose();
    throw err;
  }
};
