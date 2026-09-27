// Remediation kind detection, object rendering, dependency summaries, apply order.
import { ComplianceRemediation, nodePoolFromScanName, SCAN_NAME_LABEL } from './models';
import { isValidK8sName } from './names';
import { isString, stripFormatChars, stripInvisibleText } from './parse';
import { formatList, textCollator } from './text';

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
// applies (a KubeletConfig, for one), plus a partially rendered remediation with no
// kind yet. So fall back to the scan-name label (the same signal the operator's
// poolFromRemediation uses) for ANY non-MachineConfig kind, not only an empty one,
// or such a remediation gets no reboot warning and is silently excluded from the
// batch reboot-coalescing while still rebooting the pool. Pool suffix must be
// DNS-1123 (the operator's validK8sName) so the UI never marks a remediation
// batch-eligible that the controller cannot pause. Kept in lockstep with the
// operator: node iff a MachineConfig, or a valid "…-node-<pool>" scan name.
export const isNodeRemediation = (rem: ComplianceRemediation): boolean => {
  if (rem.spec?.current?.object?.kind === 'MachineConfig') {
    return true;
  }
  const pool = nodePoolFromScanName(rem.metadata?.labels?.[SCAN_NAME_LABEL] ?? '');
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
  const obj = rem.spec?.current?.object;
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
//   depends-on:     comma-separated XCCDF rule IDs that must PASS first
//   depends-on-obj: JSON list of {apiVersion,kind,name,namespace?} objects
//   unset-value:    comma-separated variable names still required
// Falls back to status.errorMessage when annotations are empty so Error and
// MissingDependencies with only a status message still surface something.
// Untrusted cluster data: never throws on malformed JSON / hostile strings.
// Format characters are stripped from every part, matching the CSV export and
// the HTML report: without it a bidirectional override in an annotation name
// reverses the rendered row, while the same value exported to CSV or the report
// comes out clean.
// The parts are joined with the locale's list punctuation, not a literal ", ",
// because the result is interpolated into a translated sentence: in Arabic the
// separator is "، " and in Japanese the list has none.
export const missingDependencySummary = (
  rem: ComplianceRemediation,
  locale?: string,
): string | null => {
  const ann = rem.metadata.annotations ?? {};
  const parts: string[] = [];
  // Coerce then strip: an annotation is untyped CR text, and a tampered
  // non-string must not throw .split on it.
  const read = (key: string): string => {
    const v = ann[key];
    return stripFormatChars(isString(v) ? v : '');
  };
  // Comma-separated annotation values are trimmed and dropped when empty, the
  // same on every annotation that carries one.
  const csvList = (text: string): string[] =>
    text.split(',').map((s) => s.trim()).filter(Boolean);
  const field = (v: unknown): string => stripFormatChars(isString(v) ? v.trim() : '');

  parts.push(...csvList(read(dependsOnAnn)));

  const rawObj = read(dependsOnObjAnn).trim();
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
          // Narrow, then strip: JSON.parse yields whatever the annotation held,
          // so a name is arbitrary untrusted text on this path.
          const name = field(d.name);
          const kind = field(d.kind);
          const ns = field(d.namespace);
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

  parts.push(...csvList(read(unsetValueAnn)).map((v) => `value:${v}`));

  if (parts.length) {
    return formatList(parts, locale);
  }
  const err = rem.status?.errorMessage;
  // Free text from the Compliance Operator, so the prose strip: a ZWJ or ZWNJ in
  // it is script content (emoji sequence, Arabic/Persian compound), not an
  // attempt to hide a character.
  return isString(err) ? stripInvisibleText(err).trim() || null : null;
};

// Sort key for guided remediation: applyable remediations first so prerequisite
// fixes appear above MissingDependencies rows (openspec guided-remediation).
// Stable by name within each group. Names are untrusted list-watch data: coerce
// so a partial/tampered item cannot throw mid-sort.
// textCollator, not a bare new Intl.Collator(): it carries the console locale
// (so a German console orders like the rest of the page, not like the browser's
// default) and numeric:true, so rule_2 sorts before rule_10 as it does in the
// Profiles catalog. The locale is a parameter, not a module-load constant:
// a comparator bound to the default locale at import time keeps ordering by
// the browser's language for the life of the tab, which disagrees with every
// other sorted list on the page once the console locale differs.
export const compareRemediationsForApplyOrder = (
  locale?: string,
): ((a: ComplianceRemediation, b: ComplianceRemediation) => number) => {
  const collator = textCollator(locale);
  const blocked = (r: ComplianceRemediation) =>
    r.status?.applicationState === 'MissingDependencies' ? 1 : 0;
  return (a, b) => {
    const d = blocked(a) - blocked(b);
    if (d !== 0) {
      return d;
    }
    return collator.compare(
      isString(a.metadata?.name) ? a.metadata.name : '',
      isString(b.metadata?.name) ? b.metadata.name : '',
    );
  };
};
