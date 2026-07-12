import * as React from 'react';
import { Tooltip } from '@patternfly/react-core';

// Visible keyboard focus for the tip wrapper (disabled children never show a
// focus ring). PatternFly tokens so dark/light themes stay readable.
const focusStyle = (el: HTMLElement, on: boolean) => {
  if (on) {
    el.style.outline = '2px solid var(--pf-t--global--border--color--brand--default)';
    el.style.outlineOffset = '2px';
  } else {
    el.style.outline = '';
    el.style.outlineOffset = '';
  }
};

// Focus ring for keyboard-focusable scroll regions (tabIndex=0). focusin/out
// bubble, so only outline when the region itself is focused, not a descendant
// button/link. Shared by Overview "Recent changes" and Remediations table wrap.
export const regionFocusProps = {
  onFocus: (e: React.FocusEvent<HTMLElement>) => {
    if (e.target !== e.currentTarget) return;
    focusStyle(e.currentTarget, true);
  },
  onBlur: (e: React.FocusEvent<HTMLElement>) => {
    if (e.target !== e.currentTarget) return;
    focusStyle(e.currentTarget, false);
  },
};

// Disabled controls need a wrapper so tooltips still receive pointer and
// keyboard focus (disabled buttons do not fire either). tabIndex keeps the
// reason discoverable for keyboard users (Tooltip trigger includes focus).
// aria-label exposes the same reason to screen readers that do not announce
// tooltips on focus alone.
export const withDisabledTip = (
  tip: string | undefined,
  child: React.ReactElement,
): React.ReactElement =>
  tip ? (
    <Tooltip content={tip}>
      <span
        style={{ display: 'inline-block' }}
        tabIndex={0}
        role="group"
        aria-label={tip}
        onFocus={(e) => focusStyle(e.currentTarget, true)}
        onBlur={(e) => focusStyle(e.currentTarget, false)}
      >
        {child}
      </span>
    </Tooltip>
  ) : (
    child
  );
