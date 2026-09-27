import { AccessGate, mayWrite } from './permissions';

const allowed: AccessGate = { allowed: true };
const denied: AccessGate = { allowed: false };
// An unresolved review reaches the console as allowed: false, not as a separate
// pending state: see the AccessGate docstring.
const resolving: AccessGate = { allowed: false };

// The deny side is the product: a control's isDisabled is a render decision, so
// every mutation re-checks at the request boundary. These pin the cases a
// viewer or a revoked permission must hit.
describe('mayWrite', () => {
  it('allows a confirmed permission', () => {
    expect(mayWrite(allowed)).toBe(true);
  });

  it('denies a revoked permission', () => {
    expect(mayWrite(denied)).toBe(false);
  });

  it('denies while the review is unresolved: an in-flight review is not an authorization', () => {
    expect(mayWrite(resolving)).toBe(false);
  });
});
