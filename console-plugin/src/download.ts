// Strip path separators, control characters, relative segments, and bidirectional
// overrides so a hostile filename cannot bias the browser save path or spoof
// extensions via RTL (defense in depth; callers use fixed names today).
// Leading dots become underscores (no hidden-file names). Cap length so a huge
// CR-derived name cannot create an oversized Content-Disposition path.
const safeDownloadName = (filename: string): string => {
  const cleaned = filename
    .replace(/[/\\:\0-\x1f\x7f]/g, '_')
    // BIDI / isolate controls (U+200E–U+200F, U+202A–U+202E, U+2066–U+2069)
    // and zero-width / BOM (U+200B–U+200D, U+FEFF) that can spoof extensions.
    .replace(/[\u200B-\u200D\u200E\u200F\u202A-\u202E\u2066-\u2069\uFEFF]/g, '_')
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
