// Remediation kind detection, object rendering, dependency summaries, apply order.
import { ComplianceRemediation, nodePoolFromScanName, SCAN_NAME_LABEL } from './models';
import { isValidK8sName } from './names';
import { isString } from './parse';

// Fields of a compliance-operator depends-on-obj JSON entry; values are
// untrusted annotation text, so each field is narrowed before use.
interface DependencyRef {
  kind?: unknown;
  name?: unknown;
  namespace?: unknown;
}

const isDependencyRef = (v: unknown): v is DependencyRef =>
  v !== null && typeof v === 'object';

// CO annotations naming unmet remediation dependencies (see compliance-operator
// RemediationDependencyAnnotation / RemediationObjectDependencyAnnotation).
const dependsOnAnn = 'compliance.openshift.io/depends-on';
const dependsOnObjAnn = 'compliance.openshift.io/depends-on-obj';
const unsetValueAnn = 'compliance.openshift.io/unset-value';

// A node remediation reboots nodes when applied. A MachineConfig always does.
// So does any other object rendered by a node scan ("…-node-<pool>") that the MCO
// applies, notably a KubeletConfig, plus a partially rendered remediation with no
// kind yet. So fall back to the scan-name label (the same signal the operator's
// poolFromRemediation uses) for ANY non-MachineConfig kind, not only an empty one,
// or such a remediation gets no reboot warning and is silently excluded from the
// batch reboot-coalescing while still rebooting the pool. Pool suffix must be
// DNS-1123 (operator validMCPPoolName) so the UI never marks a remediation
// batch-eligible that the controller cannot pause. Kept in lockstep with the
// operator: node iff a MachineConfig, or a valid "…-node-<pool>" scan name.
export const isNodeRemediation = (rem: ComplianceRemediation): boolean => {
  if (rem.spec.current?.object?.kind === 'MachineConfig') {
    return true;
  }
  const pool = nodePoolFromScanName(rem.metadata.labels?.[SCAN_NAME_LABEL] ?? '');
  return pool != null && isValidK8sName(pool);
};

// Pretty-printed rendered object for the remediation detail view.
// Untrusted CR data: JSON.stringify can throw on circular graphs or non-JSON
// values (e.g. bigint). Never let that crash the remediations detail modal.
// Unserializable objects return a non-empty marker so the UI does not collapse
// them into the same "No rendered object" empty state as a missing object.
// Sentinel returned when the rendered object cannot be serialized (circular /
// bigint graph - never produced by the Compliance Operator, but CR data is
// untrusted). remediation.ts is a pure helper with no translator, so the
// component maps this to a localized message; it is never displayed verbatim.
// Leading NUL makes the marker unforgeable: JSON.stringify output can never
// start with one. Written as the \0 escape (never a raw NUL byte) so this file
// stays plain text for rg / file / editors.
export const REMEDIATION_OBJECT_UNSERIALIZABLE = '\0unserializable';

export const remediationObjectText = (rem: ComplianceRemediation): string => {
  const obj = rem.spec.current?.object;
  if (!obj) {
    return '';
  }
  try {
    return JSON.stringify(obj, null, 2);
  } catch {
    return REMEDIATION_OBJECT_UNSERIALIZABLE;
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
      // SAFETY: JSON.parse validated the array shape; each element is narrowed to
      // DependencyRef by the guard below before any field is consumed.
      const deps = JSON.parse(rawObj);
      if (Array.isArray(deps)) {
        for (const d of deps) {
          if (!isDependencyRef(d)) {
            continue;
          }
          const name = isString(d.name) ? d.name.trim() : '';
          const kind = isString(d.kind) ? d.kind.trim() : '';
          const ns = isString(d.namespace) ? d.namespace.trim() : '';
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
  const an = isString(a.metadata?.name) ? a.metadata.name : '';
  const bn = isString(b.metadata?.name) ? b.metadata.name : '';
  return an.localeCompare(bn);
};
