// Remediation kind detection, object rendering, dependency summaries, apply order.
import { ComplianceRemediation, nodePoolFromScanName, SCAN_NAME_LABEL } from './models';
import { isValidK8sName } from './names';

// CO annotations naming unmet remediation dependencies (see compliance-operator
// RemediationDependencyAnnotation / RemediationObjectDependencyAnnotation).
const dependsOnAnn = 'compliance.openshift.io/depends-on';
const dependsOnObjAnn = 'compliance.openshift.io/depends-on-obj';
const unsetValueAnn = 'compliance.openshift.io/unset-value';

// A node remediation renders into a MachineConfig; applying it reboots nodes.
// Prefer the rendered object kind; when kind is empty, fall back to the scan-name
// label the same way the operator's poolFromRemediation does ("…-node-<pool>"),
// so reboot warnings and batch eligibility stay accurate for partially rendered
// remediations. Pool suffix must be DNS-1123 (operator validMCPPoolName) so the
// UI does not mark batch-eligible remediations the controller cannot pause.
// A known non-MachineConfig kind is never treated as node.
export const isNodeRemediation = (rem: ComplianceRemediation): boolean => {
  const kind = rem.spec.current?.object?.kind;
  if (kind === 'MachineConfig') {
    return true;
  }
  if (kind) {
    return false;
  }
  const pool = nodePoolFromScanName(rem.metadata.labels?.[SCAN_NAME_LABEL] ?? '');
  return pool != null && isValidK8sName(pool);
};

// Pretty-printed rendered object for the remediation detail view.
// Untrusted CR data: JSON.stringify can throw on circular graphs or non-JSON
// values (e.g. bigint). Never let that crash the remediations detail modal.
// Unserializable objects return a non-empty marker so the UI does not collapse
// them into the same "No rendered object" empty state as a missing object.
export const remediationObjectText = (rem: ComplianceRemediation): string => {
  const obj = rem.spec.current?.object;
  if (!obj) {
    return '';
  }
  try {
    return JSON.stringify(obj, null, 2);
  } catch {
    return '/* unserializable remediation object */';
  }
};

// Human-readable summary of why a remediation is blocked on dependencies.
// Sources (Compliance Operator):
//   depends-on     — comma-separated XCCDF rule IDs that must PASS first
//   depends-on-obj — JSON list of {apiVersion,kind,name,namespace?} objects
//   unset-value    — comma-separated variable names still required
// Falls back to status.errorMessage when annotations are empty so Error and
// MissingDependencies with only a status message still surface something.
// Untrusted cluster data: never throws on malformed JSON / hostile strings.
export const missingDependencySummary = (rem: ComplianceRemediation): string | null => {
  const ann = rem.metadata.annotations ?? {};
  const parts: string[] = [];

  for (const raw of (ann[dependsOnAnn] ?? '').split(',')) {
    const id = raw.trim();
    if (id) {
      parts.push(id);
    }
  }

  const rawObj = (ann[dependsOnObjAnn] ?? '').trim();
  if (rawObj) {
    try {
      const deps = JSON.parse(rawObj) as unknown;
      if (Array.isArray(deps)) {
        for (const d of deps) {
          if (!d || typeof d !== 'object') {
            continue;
          }
          const o = d as { kind?: unknown; name?: unknown; namespace?: unknown };
          const name = typeof o.name === 'string' ? o.name.trim() : '';
          const kind = typeof o.kind === 'string' ? o.kind.trim() : '';
          const ns = typeof o.namespace === 'string' ? o.namespace.trim() : '';
          if (!name && !kind) {
            continue;
          }
          const nsPrefix = ns ? `${ns}/` : '';
          parts.push(kind ? `${kind} ${nsPrefix}${name}`.trim() : `${nsPrefix}${name}`);
        }
      } else {
        // Non-array JSON (object/string/number): surface raw so the admin can act.
        parts.push(rawObj);
      }
    } catch {
      // Malformed annotation: surface the raw value so the admin can still act.
      parts.push(rawObj);
    }
  }

  for (const raw of (ann[unsetValueAnn] ?? '').split(',')) {
    const v = raw.trim();
    if (v) {
      parts.push(`value:${v}`);
    }
  }

  if (parts.length) {
    return parts.join(', ');
  }
  const err = rem.status?.errorMessage?.trim();
  return err || null;
};

// Sort key for guided remediation: applyable remediations first so prerequisite
// fixes appear above MissingDependencies rows (openspec guided-remediation).
// Stable by name within each group. Names are untrusted list-watch data: coerce
// so a partial/tampered item cannot throw mid-sort.
export const compareRemediationsForApplyOrder = (
  a: ComplianceRemediation,
  b: ComplianceRemediation,
): number => {
  const blocked = (r: ComplianceRemediation) =>
    r.status?.applicationState === 'MissingDependencies' ? 1 : 0;
  const d = blocked(a) - blocked(b);
  if (d !== 0) {
    return d;
  }
  const an = typeof a.metadata?.name === 'string' ? a.metadata.name : '';
  const bn = typeof b.metadata?.name === 'string' ? b.metadata.name : '';
  return an.localeCompare(bn);
};
