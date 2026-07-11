## 1. API + scoring (operator)

- [x] 1.1 Extend WaiverEntry with optional `requestedBy`, `approvedBy`, `reviewBy`, `expiresAt` (RFC3339 date); regen CRD + deepcopy + bundle
- [x] 1.2 aggregateStatus: skip expired waivers (expiresAt < now) so the check counts by raw status; unit test expired-vs-active
- [x] 1.3 Add `spec.scoring.mode` enum (Flat default | SeverityWeighted); regen CRD
- [x] 1.4 Implement severity-weighted score (weight PASS/FAIL by severity); keep Flat path identical; unit tests pin both modes + range 0..100
- [x] 1.5 Record last-two status per owned check in status (bounded map); compute regressed (PASS->FAIL) and new-FAIL sets; unit test transitions + bounded size
- [x] 1.6 Extend history to per-profile score history (reuse 30-cap ring); unit test
- [x] 1.7 Metrics: add severity-weighted score series (or reuse gauge with a mode label); update TestPublishMetrics
- [x] 1.8 Fuzz any new untrusted-input parsing (waiver date parse, severity map lookup)

## 2. Console: governance + visibility

- [x] 2.1 Waiver modal: reason + requester + approver + expiry/review date inputs; patch WaiverEntry with the new fields; jest for the patch shape
- [x] 2.2 Surface expired/expiring waivers: Results shows an expired waiver as failing with metadata; Overview shows an expiring-soon count; jest + Playwright
- [ ] 2.3 Regressions view (new tab or Overview section): list PASS->FAIL and new-FAIL from status, deep-link each; first-scan empty state; jest + Playwright
- [x] 2.4 Schedule editor: cron input with validation, gated on clusterbaselines patch, patches spec.schedule; jest for validation + patch
- [x] 2.5 Per-profile trend: render each profile card's own trend line from per-profile history; jest

## 3. Guided remediation

- [x] 3.1 Operator: batch-apply action that pauses target MachineConfigPool(s), sets apply on the selected remediations, resumes (guaranteed resume on failure); RBAC for machineconfigpools patch; unit test pause/apply/resume + resume-on-error
- [x] 3.2 Surface MissingDependencies/Outdated as blocked with the missing dependency named; order so prerequisites apply first; jest on state mapping
- [x] 3.3 Console: multi-select + Batch apply flow with reboot-once confirmation and MCP-pause explanation; Playwright asserts the confirm + selection
- [ ] 3.4 e2e (Go, live): batch apply pauses then resumes the pool; pool never left paused

## 4. TailoredProfile authoring (console)

- [x] 4.1 TailoredProfile editor: pick base profile, enable/disable rules, set variable values; k8sCreate/k8sPatch the TailoredProfile CR; gate on RBAC
- [x] 4.2 On save, add the name to spec.tailoredProfiles so it binds and scores; hide the UI when the TailoredProfile CRD is absent
- [x] 4.3 jest for the CR body build; Playwright for create + bind (guarded/skipped if CRD missing)

## 5. Packaging artifacts

- [x] 5.1 Helm chart: CRD, RBAC, operator Deployment, console plugin, default-CR toggle, configurable images/profiles; `helm template` + `helm lint` in CI; uninstall leaves the Compliance Operator
- [x] 5.2 Native console dashboard (`console.openshift.io/dashboard` ConfigMap in openshift-config-managed) from baseline_security_compliance_score + checks; renders in Observe -> Dashboards, no Grafana; document the UWM + ServiceMonitor prerequisite
- [x] 5.3 NSA/CISA hardening TailoredProfile YAML + a documented rule mapping (note guidance with no rule); apply/bind smoke
- [x] 5.4 Compliance report export (printable HTML) from watched data: score, per-profile, failing checks, active waivers w/ attribution; untrusted text rendered as text; jest for the report model builder

## 6. Score-trend + informer

- [x] 6.1 Console trend card / extend the existing history chart for the shipped in-console trend (pairs with the 5.2 console dashboard)
- [ ] 6.2 (DEFERRED - polling works; needs a lazy informer tolerating CRD-absent startup) Dynamic informer: watch ComplianceCheckResult/Scan/Remediation once CRDs exist, map events to the singleton reconcile; tolerate NoKindMatch at startup; keep poll fallback
- [ ] 6.3 e2e/unit: reconcile fires on scan-result change without waiting for the poll; manager starts without the CRDs

## 7. Docs + test-plan

- [x] 7.1 Update SPEC.md/README/TODO with the new features; refresh screenshots (light+dark) for new UI
- [x] 7.2 Update docs/TEST-PLAN.md: tick new coverage, add the new capabilities' cases and the run ledger
- [x] 7.3 Bump version; validate bundle; full battery (unit+fuzz+jest+promtool+Go e2e+Playwright) green on the live cluster
