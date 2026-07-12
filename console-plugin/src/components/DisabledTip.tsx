import * as React from 'react';
import { Tooltip } from '@patternfly/react-core';

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
      >
        {child}
      </span>
    </Tooltip>
  ) : (
    child
  );
