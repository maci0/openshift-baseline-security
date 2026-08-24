import { ComplianceRemediation, RemediationObject } from './models';
import { isValidK8sName } from './names';
import { REMEDIATION_OBJECT_UNSERIALIZABLE, compareRemediationsForApplyOrder, isNodeRemediation, missingDependencySummary, remediationObjectText } from './remediation';
import { randomString } from './testing/fuzz';
import { isString } from './parse';

// Primitive-contract checks live in type guards (the one allowed typeof home).
const isBool = (v: unknown): v is boolean => typeof v === 'boolean';

// Self-referencing rendered object stays fully typed (no dictionary escape).
type CircularObject = RemediationObject & { self?: CircularObject };

describe('remediation helpers', () => {
  const rem = (
    kind?: string,
    obj?: RemediationObject,
    extra?: Partial<ComplianceRemediation>,
  ): ComplianceRemediation =>
    // SAFETY: fixture mirrors a CO ComplianceRemediation CR; partial/hostile fields deliberate.
    ({
      metadata: { name: 'r', namespace: 'openshift-compliance', ...extra?.metadata },
      spec: {
        apply: false,
        current: obj ? { object: obj } : kind ? { object: { kind } } : undefined,
        ...extra?.spec,
      },
      status: extra?.status,
    }) as ComplianceRemediation;

  it('isNodeRemediation detects MachineConfig', () => {
    expect(isNodeRemediation(rem('MachineConfig'))).toBeTruthy();
    expect(isNodeRemediation(rem('APIServer'))).toBeFalsy();
    expect(isNodeRemediation(rem())).toBeFalsy();
  });
  // Parity with operator poolFromRemediation: empty kind + node scan-name is a
  // node remediation (reboot warning / batch eligibility).
  it('isNodeRemediation falls back to scan-name when kind is empty', () => {
    expect(
      isNodeRemediation(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis-node-worker' },
          },
        }),
      ),
    ).toBeTruthy();
    expect(
      isNodeRemediation(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis' },
          },
        }),
      ),
    ).toBeFalsy();
    // Operator validMCPPoolName: non-DNS-1123 pool suffix is not a batch target.
    expect(
      isNodeRemediation(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis-node-UPPER' },
          },
        }),
      ),
    ).toBeFalsy();
    // A non-MachineConfig kind rendered by a node scan (e.g. a KubeletConfig, which
    // the MCO applies with a reboot) is STILL a node remediation: the "…-node-<pool>"
    // scan-name is authoritative, matching operator poolFromRemediation. The kind
    // must not short-circuit the fallback, or such a remediation would reboot the
    // pool with no warning and outside the batch pause window.
    expect(
      isNodeRemediation(
        rem('KubeletConfig', undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis-node-worker' },
          },
        }),
      ),
    ).toBeTruthy();
    // But a platform kind on a scan with no "-node-" is not a node remediation.
    expect(
      isNodeRemediation(
        rem('APIServer', undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            labels: { 'compliance.openshift.io/scan-name': 'ocp4-cis-api-server' },
          },
        }),
      ),
    ).toBeFalsy();
  });
  it('remediationObjectText pretty-prints the object, empty when absent', () => {
    expect(
      remediationObjectText(
        rem('MachineConfig', { kind: 'MachineConfig', metadata: { name: 'mc' } }),
      ),
    ).toContain('"kind": "MachineConfig"');
    expect(remediationObjectText(rem())).toBe('');
  });
  it('remediationObjectText does not throw on circular rendered objects', () => {
    const circular: CircularObject = { kind: 'MachineConfig' };
    circular.self = circular;
    // Distinct sentinel (not '' for absent): the component maps it to a
    // translated message rather than showing raw untranslated text.
    expect(remediationObjectText(rem('MachineConfig', circular))).toBe(
      REMEDIATION_OBJECT_UNSERIALIZABLE,
    );
  });
  // openspec guided-remediation: MissingDependencies must name the dependency.
  it('missingDependencySummary reads depends-on, depends-on-obj, and unset-value', () => {
    expect(missingDependencySummary(rem())).toBeNull();
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: {
              'compliance.openshift.io/depends-on': 'xccdf_org.ssgproject.content_rule_a, rule_b',
            },
          },
        }),
      ),
    ).toBe('xccdf_org.ssgproject.content_rule_a, rule_b');
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: {
              'compliance.openshift.io/depends-on-obj': JSON.stringify([
                {
                  apiVersion: 'v1',
                  kind: 'ConfigMap',
                  name: 'foo',
                  namespace: 'openshift-compliance',
                },
              ]),
            },
          },
        }),
      ),
    ).toBe('ConfigMap openshift-compliance/foo');
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: { 'compliance.openshift.io/unset-value': 'var_password_min_len' },
          },
        }),
      ),
    ).toBe('value:var_password_min_len');
    // Malformed JSON: surface raw rather than empty.
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: { 'compliance.openshift.io/depends-on-obj': '{not-json' },
          },
        }),
      ),
    ).toBe('{not-json');
    // Valid JSON that is not an array (hostile hand-edit): surface raw.
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: {
              'compliance.openshift.io/depends-on-obj': '{"kind":"ConfigMap"}',
            },
          },
        }),
      ),
    ).toBe('{"kind":"ConfigMap"}');
    // Junk entries inside a valid array are skipped; good entries still surface.
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'openshift-compliance',
            annotations: {
              'compliance.openshift.io/depends-on-obj': JSON.stringify([
                null,
                'not-an-object',
                { namespace: 'ns-only' },
                { kind: 'Config', name: '' },
                { kind: 'Secret', name: 'tok' },
              ]),
            },
          },
        }),
      ),
    ).toBe('Config, Secret tok');
    // Fall back to status.errorMessage when annotations are empty.
    expect(
      missingDependencySummary(
        rem(undefined, undefined, {
          status: { applicationState: 'Error', errorMessage: 'apply failed: conflict' },
        }),
      ),
    ).toBe('apply failed: conflict');
  });
  it('compareRemediationsForApplyOrder puts MissingDependencies last', () => {
    const a = rem(undefined, undefined, {
      metadata: { name: 'z-ready', namespace: 'ns' },
      status: { applicationState: 'NotApplied' },
    });
    const b = rem(undefined, undefined, {
      metadata: { name: 'a-blocked', namespace: 'ns' },
      status: { applicationState: 'MissingDependencies' },
    });
    const c = rem(undefined, undefined, {
      metadata: { name: 'm-ready', namespace: 'ns' },
      status: { applicationState: 'NotApplied' },
    });
    const sorted = [b, a, c].sort(compareRemediationsForApplyOrder);
    expect(sorted.map((r) => r.metadata.name)).toEqual(['m-ready', 'z-ready', 'a-blocked']);
  });
  it('fuzz: returns a string and never throws for arbitrary rendered objects', () => {
    for (let i = 0; i < 1000; i++) {
      const obj = {
        kind: randomString(i % 12),
        [randomString((i % 6) + 1)]: i,
        nested: { a: [randomString(i % 5)], b: i % 2 === 0 },
        weird: i % 13 === 0 ? undefined : randomString(i % 10),
      };
      const out = remediationObjectText(rem('X', obj));
      expect(isString(out)).toBeTruthy();
      expect(missingDependencySummary(rem('X', obj))).toBeNull();
    }
  });
  it('fuzz: missingDependencySummary never throws on hostile annotations', () => {
    for (let i = 0; i < 500; i++) {
      const summary = missingDependencySummary(
        rem(undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'ns',
            annotations: {
              'compliance.openshift.io/depends-on': randomString(i % 40),
              'compliance.openshift.io/depends-on-obj': randomString(i % 40),
              'compliance.openshift.io/unset-value': randomString(i % 20),
            },
          },
          status: { errorMessage: randomString(i % 20) },
        }),
      );
      expect(summary === null || isString(summary)).toBeTruthy();
    }
  });
  // Kind + scan-name are untrusted CO fields; reboot/batch eligibility must not
  // throw, and node-ness is MachineConfig OR a DNS-valid "-node-<pool>" scan name.
  it('fuzz: isNodeRemediation never throws and matches the node-scan invariant', () => {
    for (let i = 0; i < 500; i++) {
      const kind =
        i % 7 === 0 ? 'MachineConfig' : i % 7 === 1 ? '' : randomString(i % 12);
      const scan =
        i % 5 === 0
          ? `ocp4-cis-node-${randomString((i % 8) + 1)}`
          : randomString(i % 20);
      const out = isNodeRemediation(
        rem(kind || undefined, undefined, {
          metadata: {
            name: 'r',
            namespace: 'ns',
            labels: { 'compliance.openshift.io/scan-name': scan },
          },
        }),
      );
      expect(isBool(out)).toBeTruthy();
      // Exact invariant (lockstep with operator poolFromRemediation): a
      // MachineConfig is always a node remediation; any other kind is node iff its
      // scan name ends in a DNS-valid "-node-<pool>". Non-vacuous: asserts the
      // real boolean, so a kind that wrongly short-circuits the scan fallback
      // (the round-10 bug) would fail here.
      const di = scan.lastIndexOf('-node-');
      const pool = di < 0 ? '' : scan.slice(di + '-node-'.length);
      const want = kind === 'MachineConfig' || (pool !== '' && isValidK8sName(pool));
      expect(out).toBe(want);
    }
  });
  it('fuzz: compareRemediationsForApplyOrder is antisymmetric and total', () => {
    for (let i = 0; i < 200; i++) {
      const a = rem(undefined, undefined, {
        metadata: { name: randomString((i % 10) + 1), namespace: 'ns' },
        status: {
          applicationState: i % 3 === 0 ? 'MissingDependencies' : 'NotApplied',
        },
      });
      const b = rem(undefined, undefined, {
        metadata: { name: randomString((i % 10) + 1), namespace: 'ns' },
        status: {
          applicationState: i % 5 === 0 ? 'MissingDependencies' : 'NotApplied',
        },
      });
      const ab = compareRemediationsForApplyOrder(a, b);
      const ba = compareRemediationsForApplyOrder(b, a);
      expect(Number.isFinite(ab)).toBeTruthy();
      expect(Number.isFinite(ba)).toBeTruthy();
      let antisymmetry: string | undefined;
      if (ab === 0 && ba !== 0) {
        antisymmetry = `compare(a, b) = 0 but compare(b, a) = ${ba}`;
      } else if (ab !== 0 && Math.sign(ab) !== -Math.sign(ba)) {
        antisymmetry = `sign(compare(a, b) = ${ab}) != -sign(compare(b, a) = ${ba})`;
      }
      expect(antisymmetry).toBeUndefined();
      expect(compareRemediationsForApplyOrder(a, a)).toBe(0);
    }
  });
  // Partial list-watch items must not throw mid-sort.
  it('compareRemediationsForApplyOrder tolerates missing names', () => {
    const missingName: unknown = undefined;
    // SAFETY: partial list items can omit metadata.name at runtime; sort must not throw.
    const a = rem(undefined, undefined, {
      metadata: { name: missingName as string, namespace: 'ns' },
    });
    const b = rem(undefined, undefined, {
      metadata: { name: 'b', namespace: 'ns' },
    });
    expect(() => compareRemediationsForApplyOrder(a, b)).not.toThrow();
    expect(Number.isFinite(compareRemediationsForApplyOrder(a, b))).toBeTruthy();
  });
});
