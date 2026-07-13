import { restoreFocus } from './focus';

// restoreFocus is the WCAG 2.4.3 focus-recovery helper: focus the modal trigger
// if it is still connected, else a stable fallback (the trigger can unmount on
// success), and never throw. It only reads isConnected/focus and defers via
// requestAnimationFrame, so fake objects + a synchronous rAF stub exercise every
// branch without a DOM (this project runs jest in the node environment).
describe('restoreFocus', () => {
  const origWindow = (global as { window?: unknown }).window;
  beforeEach(() => {
    (global as { window?: unknown }).window = {
      requestAnimationFrame: (cb: (t: number) => void) => {
        cb(0);
        return 0;
      },
    };
  });
  afterEach(() => {
    (global as { window?: unknown }).window = origWindow;
  });

  const fakeEl = (isConnected: boolean) => {
    const focus = jest.fn();
    return { el: { isConnected, focus } as unknown as HTMLElement, focus };
  };

  it('focuses the trigger when it is still connected', () => {
    const { el, focus } = fakeEl(true);
    restoreFocus(el);
    expect(focus).toHaveBeenCalledTimes(1);
  });

  it('focuses the fallback (not the detached trigger) when the trigger is gone', () => {
    // Dropping the isConnected guard would call the detached trigger's focus()
    // (a real-DOM no-op that drops focus to <body>) instead of the fallback.
    const { el, focus: triggerFocus } = fakeEl(false);
    const fbFocus = jest.fn();
    const fallback = { current: { focus: fbFocus } as unknown as HTMLElement };
    restoreFocus(el, fallback);
    expect(triggerFocus).not.toHaveBeenCalled();
    expect(fbFocus).toHaveBeenCalledTimes(1);
  });

  it('does nothing and does not throw when detached with no fallback', () => {
    const { el, focus } = fakeEl(false);
    expect(() => restoreFocus(el)).not.toThrow();
    expect(focus).not.toHaveBeenCalled();
  });

  it('does not throw on a null trigger', () => {
    expect(() => restoreFocus(null)).not.toThrow();
  });
});
