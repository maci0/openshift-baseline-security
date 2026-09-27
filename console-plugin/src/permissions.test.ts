import { AccessGate, mayWrite } from './permissions';

const allowed: AccessGate = { allowed: true, loading: false };
const denied: AccessGate = { allowed: false, loading: false };
const resolving: AccessGate = { allowed: false, loading: true };

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

  it('ignores the loading flag once the review has resolved', () => {
    expect(mayWrite({ allowed: true, loading: false })).toBe(true);
    expect(mayWrite({ allowed: false, loading: true })).toBe(false);
  });
});
