import { restoreFocus } from './focus';

// restoreFocus is the WCAG 2.4.3 focus-recovery helper: focus the modal trigger
// if it is still connected, else a stable fallback (the trigger can unmount on
// success), and never throw. It only reads isConnected/focus, the live
// document.activeElement, and defers via requestAnimationFrame, so fake objects
// + a controllable rAF stub exercise every branch without a DOM (this project
// runs jest in the node environment).

// Structural stand-in for the element surface restoreFocus touches; the helper
// dereferences nothing beyond isConnected and focus at runtime.
type ElementLike = { isConnected?: boolean; focus(): void };

// Structural stand-in for the dialog surface restoreFocus matches against: it
// only ever holds these in a Set and compares one by identity.
type DialogLike = { name: string };

describe('restoreFocus', () => {
  // SAFETY: this suite runs in the jest node environment, where globalThis
  // carries no window or document binding; restoreFocus reads exactly
  // window.requestAnimationFrame, window.cancelAnimationFrame, and the
  // document surface stubbed below (querySelectorAll, activeElement), so this
  // one cast carries both members.
  const globalWithWindow = global as { window?: unknown; document?: unknown };
  const origWindow = globalWithWindow.window;
  const origDocument = globalWithWindow.document;
  const pendingFrames: Map<number, (t: number) => void> = new Map();
  let nextFrame = 0;
  beforeEach(() => {
    pendingFrames.clear();
    nextFrame = 0;
    globalWithWindow.window = {
      requestAnimationFrame: (cb: (t: number) => void) => {
        const id = nextFrame++;
        // Synchronous by default so the existing cases read as "the frame
        // fired"; a case that needs a pending frame swaps this out below.
        cb(0);
        return id;
      },
      cancelAnimationFrame: (id: number) => {
        pendingFrames.delete(id);
      },
    };
  });
  afterEach(() => {
    globalWithWindow.window = origWindow;
    globalWithWindow.document = origDocument;
  });

  // Defer the frame so the test controls when the callback runs, which is what
  // the dialog and cancel cases need.
  const deferFrames = () => {
    globalWithWindow.window = {
      requestAnimationFrame: (cb: (t: number) => void) => {
        const id = nextFrame++;
        pendingFrames.set(id, cb);
        return id;
      },
      cancelAnimationFrame: (id: number) => {
        pendingFrames.delete(id);
      },
    };
  };

  const runPendingFrames = () => {
    const frames = [...pendingFrames.values()];
    pendingFrames.clear();
    for (const cb of frames) cb(0);
  };

  const fakeEl = (isConnected: boolean) => {
    const focus = jest.fn();
    // SAFETY: restoreFocus only reads el.isConnected and calls el.focus(), so
    // this two-member stub satisfies every property the helper can access.
    const like = { isConnected, focus } as ElementLike;
    // SAFETY: the parameter is declared as the DOM HTMLElement type, but the
    // stub above already covers every member restoreFocus dereferences.
    const el = like as HTMLElement;
    return { el, focus };
  };

  it('focuses the trigger when it is still connected', () => {
    const { el, focus } = fakeEl(true);
    restoreFocus(el);
    // Exactly once: the preset bans toHaveBeenCalledTimes(1) and
    // toHaveBeenCalledOnce(), and "called" plus "not twice" would also pass on
    // three calls, so count the recorded calls.
    expect(focus.mock.calls).toHaveLength(1);
  });

  it('focuses the fallback (not the detached trigger) when the trigger is gone', () => {
    // Dropping the isConnected guard would call the detached trigger's focus()
    // (a real-DOM no-op that drops focus to <body>) instead of the fallback.
    const { el, focus: triggerFocus } = fakeEl(false);
    const fbFocus = jest.fn();
    // SAFETY: the fallback ref is only dereferenced as fallback.current.focus(),
    // so a one-method stub fulfills every property the helper can access.
    const like = { focus: fbFocus } as ElementLike;
    // SAFETY: RefObject<HTMLElement | null> requires the DOM element type, but
    // the stub above already covers every member restoreFocus dereferences.
    const current = like as HTMLElement;
    const fallback = { current };
    restoreFocus(el, fallback);
    expect(triggerFocus).not.toHaveBeenCalled();
    expect(fbFocus.mock.calls).toHaveLength(1);
  });

  it('does nothing and does not throw when detached with no fallback', () => {
    const { el, focus } = fakeEl(false);
    expect(() => restoreFocus(el)).not.toThrow();
    expect(focus).not.toHaveBeenCalled();
  });

  it('does not throw on a null trigger', () => {
    expect(() => restoreFocus(null)).not.toThrow();
  });

  // A dialog element stub: the helper only ever puts these in a Set, matches
  // one by identity, and reads querySelectorAll off the document.
  const fakeDialog = (name: string): DialogLike => ({ name });

  // Structural stand-in for the dialogs the helper matches by identity; it puts
  // them in a Set and compares one against activeElement.closest()'s result, so
  // nothing else is dereferenced. `focused` is null for the no-dialog case.
  const stubDocument = (dialogs: DialogLike[], focused: DialogLike | null) => {
    globalWithWindow.document = {
      querySelectorAll: () => dialogs,
      activeElement: { closest: () => focused },
    };
  };

  it('leaves focus alone when the frame lands in a dialog opened after scheduling', () => {
    // A second modal opened within the deferred frame owns focus by then;
    // restoring would pull it back to a trigger behind the new backdrop.
    deferFrames();
    const second = fakeDialog('second');
    stubDocument([], second);
    const { el, focus } = fakeEl(true);
    restoreFocus(el);
    runPendingFrames();
    expect(focus).not.toHaveBeenCalled();
  });

  it('restores while the closing dialog is still mounted for its exit transition', () => {
    // The dialog that was open when the restore was scheduled still holds focus
    // mid-unmount; it must not veto the restore the WCAG rule asks for.
    deferFrames();
    const closing = fakeDialog('closing');
    stubDocument([closing], closing);
    const { el, focus } = fakeEl(true);
    restoreFocus(el);
    runPendingFrames();
    expect(focus.mock.calls).toHaveLength(1);
  });

  it('restores focus when the frame lands outside any dialog', () => {
    deferFrames();
    stubDocument([], null);
    const { el, focus } = fakeEl(true);
    restoreFocus(el);
    runPendingFrames();
    expect(focus.mock.calls).toHaveLength(1);
  });

  it('cancel drops the frame, so an unmounted view never steals focus', () => {
    deferFrames();
    const { el, focus } = fakeEl(true);
    const cancel = restoreFocus(el);
    cancel();
    runPendingFrames();
    expect(focus).not.toHaveBeenCalled();
  });
});
