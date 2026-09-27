// The one chokepoint a console write passes through before it is sent.
//
// A control's `isDisabled` is a render decision, not an enforcement point: a tab
// can hold an open confirm modal across a permission revocation, a watch, or a
// re-render, and the click that follows would still spend the write. Every
// mutation function calls `mayWrite` first, so a denied review blocks the
// request itself and not only the button that launched it.
//
// The API server remains the real authority (k8sPatch / k8sUpdate / k8sCreate
// are authorized against the signed-in user). This gate exists so a revoked
// permission does not reach the wire at all.

/**
 * One `useAccessReview` result, as the console consumes it. The in-flight flag
 * is deliberately absent: an unresolved review reports `allowed: false`, so a
 * pending review denies without the gate having to model "loading" at all.
 */
export type AccessGate = {
  readonly allowed: boolean;
};

/** True only when the reviewed permission is confirmed. */
export const mayWrite = (gate: AccessGate): boolean => gate.allowed;
