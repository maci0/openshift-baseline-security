// Internal link that stays inside the console SPA. A bare <a href> to a console
// route leaves the single-page app and reboots the whole shell, so a cross-tab
// hop ("Go to Profiles", "Clear filters") costs a full page load and drops every
// watch the plugin had open. The href stays on the anchor so the status bar
// still shows the target and the browser's own new-tab and copy-link gestures
// keep working; only a plain left click is taken over.
import * as React from 'react';
import { useNavigate } from 'react-router';

const ConsoleLink: React.FC<{
  href: string;
  'aria-label'?: string;
  dir?: React.HTMLAttributes<HTMLAnchorElement>['dir'];
  style?: React.CSSProperties;
  children?: React.ReactNode;
}> = ({ href, children, ...rest }) => {
  // react-router, not the SDK: the console provides it to plugins as a shared
  // singleton (the SDK re-exports none of it), so this is the host's router and
  // the same instance the console's own navigation uses.
  const navigate = useNavigate();
  return (
    <a
      href={href}
      {...rest}
      onClick={(e) => {
        // Modified clicks and non-primary buttons mean "open elsewhere"; the
        // browser already does the right thing for those.
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
          return;
        }
        e.preventDefault();
        // react-router 7 returns a promise. A superseded navigation rejects it,
        // and that rejection is the router's own cancellation, not a failure
        // the click can usefully surface.
        void navigate(href);
      }}
    >
      {children}
    </a>
  );
};

export default ConsoleLink;
