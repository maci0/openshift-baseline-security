// Report a render throw to the only sink a dynamic plugin has.
//
// The console mounts an extension page without an error boundary, so a throw
// in a tab body blanks the page and leaves nothing behind but a blank tab: the
// stack is dropped where it started. A watch failure is already visible as an
// Alert, but a malformed untrusted value that throws inside a render is not,
// and it is the failure an operator asks about with nothing to attach. Report
// the component and the reason to the browser console, where a support bundle
// reads it from, and hand the reason back so the boundary can render it.
import { errorMessage } from '../errors';

// messageForRenderError names the component that threw and the reason it gave.
// A throw with no usable message is still reported: the component name and the
// captured error are the record.
export const messageForRenderError = (component: string, error: unknown): string =>
  `${component}: ${errorMessage(error) ?? 'render failed with no message'}`;

export const reportRenderError = (component: string, error: unknown): string => {
  const message = messageForRenderError(component, error);
  // The error object follows the line so the stack survives in the console,
  // which truncates a lone string in some devtools openers.
  console.error(message, error);
  return message;
};
