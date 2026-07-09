# TODO

## Done
- [x] Spec (docs/SPEC.md), operator + console-plugin scaffold, repo + CI
- [x] SNO test cluster via vmetal-openshift (OCP 4.22.0)
- [x] Reconciler refresh: 1-minute requeue so score updates after scans
- [x] Console plugin served over TLS (service-serving cert, nginx, 9443)
- [x] Finalizer (console plugin dereg) + pruning of deselected profile bindings
- [x] E2e on SNO: CO install -> CIS scan -> score 96 in status -> plugin renders
- [x] Aggregation fix: role-suffixed node scan names (found by e2e)
- [x] Degraded condition when scan PVCs stay Pending (no default StorageClass)
- [x] Console-native UI: Administration nav, HorizontalNav tabs, donut score,
      VirtualizedTable results with filters, profile cards, rescan button
- [x] Screenshots in docs/screenshots/

## Done (continued)
- [x] OLM bundle + CSV, validated (`make bundle`); catalog targets in Makefile
- [x] Result detail modal (human-readable titles, description, instructions)
- [x] Stretch S1: Remediations tab, confirmation-gated apply, auto-apply switch
- [x] Stretch S2: score history in status (30-entry ring) + trend chart

## Done (continued)
- [x] Default-create ClusterBaseline on operator start (opt out:
      BASELINE_SECURITY_SKIP_DEFAULT_CR=true)
- [x] Aggregated viewer/admin ClusterRoles (aggregate to view/cluster-reader/admin)
- [x] docs/PATTERNS.md; useAccessReview gating; nav at top of Administration;
      results scrollbar fix (single-line virtualized rows); remediation count
      on Overview
- [x] vmetal-openshift bug reported: maci0/vmetal-openshift#1

## Done (continued)
- [x] Full OLM install path verified on the SNO without quay: images + bundle +
      FBC catalog pushed to the cluster-internal registry, CatalogSource +
      Subscription in openshift-operators, CSV Succeeded, operator runs
      in-cluster and reconciles (replaced the local `make run` process).
      Found + fixed: CSV missing namespaced leader-election RBAC (leases),
      opm catalog images need the cache precomputed at build time.

## Done (continued)
- [x] API booleans replaced with string enums per api-conventions.md
      (installComplianceOperator: Automatic|Manual,
      console.managementState: Managed|Removed, remediation.apply:
      Automatic|Manual)

## Next
- [ ] Push versioned images + bundle/catalog to quay.io (needs quay login /
      robot token), swap CatalogSource image
- [ ] community-operators submission once bundle is stable
