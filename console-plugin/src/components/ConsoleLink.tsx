// Internal link that stays inside the console SPA. A bare <a href> to a console
// route leaves the single-page app and reboots the whole shell, so a cross-tab
// hop ("Go to Profiles", "Clear filters") costs a full page load and drops every
// watch the plugin had open. The href stays on the anchor so the status bar
// still shows the target and the browser's own new-tab and copy-link gestures
// keep working; only a plain left click is taken over.
import * as React from 'react';
import { useHistory } from '@openshift-console/dynamic-plugin-sdk';

const ConsoleLink: React.FC<{
  href: string;
  'aria-label'?: string;
  dir?: React.HTMLAttributes<HTMLAnchorElement>['dir'];
  style?: React.CSSProperties;
  children?: React.ReactNode;
}> = ({ href, children, ...rest }) => {
  const history = useHistory();
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
        history.push(href);
      }}
    >
      {children}
    </a>
  );
};

export default ConsoleLink;
