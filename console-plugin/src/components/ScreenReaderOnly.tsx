// Text for assistive technology only, and the em-dash placeholder that
// replaces it on screen.
//
// A bare <span> is a generic element: it takes no accessible name, so
// `aria-label` on one is not exposed and a screen reader announces the glyph
// ("dash", or nothing) instead of the state it stands for. The name has to
// ride real text, hidden from the visual layer.
//
// The hiding is inlined rather than the PatternFly screen-reader utility class
// so the text stays hidden on its own: the plugin renders into the console's
// document, and a missing global classheet would show every label on the page.
import * as React from 'react';

const HIDDEN: React.CSSProperties = {
  border: 0,
  clip: 'rect(0 0 0 0)',
  clipPath: 'inset(50%)',
  height: 1,
  margin: -1,
  overflow: 'hidden',
  padding: 0,
  position: 'absolute',
  whiteSpace: 'nowrap',
  width: 1,
};

export const ScreenReaderOnly: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <span style={HIDDEN}>{children}</span>
);
ScreenReaderOnly.displayName = 'ScreenReaderOnly';

/**
 * The rendered placeholder for an absent or unscorable value: the name for a
 * screen reader, the em dash on screen. The dash is decorative, so it is
 * hidden from the accessibility tree rather than read as a second value.
 */
export const AbsentValue: React.FC<{ label: string }> = ({ label }) => (
  <>
    <ScreenReaderOnly>{label}</ScreenReaderOnly>
    <span aria-hidden>—</span>
  </>
);
AbsentValue.displayName = 'AbsentValue';
