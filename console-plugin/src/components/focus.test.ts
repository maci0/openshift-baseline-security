import { restoreFocus } from './focus';

// restoreFocus is the WCAG 2.4.3 focus-recovery helper: focus the modal trigger
// if it is still connected, else a stable fallback (the trigger can unmount on
// success), and never throw. It only reads isConnected/focus and defers via
// requestAnimationFrame, so fake objects + a synchronous rAF stub exercise every
// branch without a DOM (this project runs jest in the node environment).
describe('restoreFocus', () => {
  // SAFETY: this suite runs in the jest node environment, where globalThis
  // carries no window binding; the only member touched here is window, and
  // restoreFocus reads exactly window.requestAnimationFrame off it.
  const globalWithWindow = global as { window?: unknown };
  const origWindow = globalWithWindow.window;
  beforeEach(() => {
    globalWithWindow.window = {
      requestAnimationFrame: (cb: (t: number) => void) => {
        cb(0);
        return 0;
      },
    };
  });
  afterEach(() => {
    globalWithWindow.window = origWindow;
  });

  const fakeEl = (isConnected: boolean) => {
    const focus = jest.fn();
    // SAFETY: restoreFocus only reads el.isConnected and calls el.focus(), so
    // this two-member stub satisfies every property the helper can access.
    const el = { isConnected, focus } as HTMLElement;
    return { el, focus };
  };

  it('focuses the trigger when it is still connected', () => {
    const { el, focus } = fakeEl(true);
    restoreFocus(el);
    expect(focus).toHaveBeenCalledOnce();
  });

  it('focuses the fallback (not the detached trigger) when the trigger is gone', () => {
    // Dropping the isConnected guard would call the detached trigger's focus()
    // (a real-DOM no-op that drops focus to <body>) instead of the fallback.
    const { el, focus: triggerFocus } = fakeEl(false);
    const fbFocus = jest.fn();
    // SAFETY: the fallback ref is only dereferenced as fallback.current.focus(),
    // so a one-method stub fulfills the full ref contract the helper uses.
    const current = { focus: fbFocus } as HTMLElement;
    const fallback = { current };
    restoreFocus(el, fallback);
    expect(triggerFocus).not.toHaveBeenCalled();
    expect(fbFocus).toHaveBeenCalledOnce();
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
