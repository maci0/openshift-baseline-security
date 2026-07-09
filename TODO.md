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

## Next
- [ ] Push versioned images + bundle/catalog to quay.io, install via CatalogSource
      (in-cluster operator deployment replaces local `make run`)
- [ ] Default-create ClusterBaseline on install (SPEC open question)
- [ ] Aggregated viewer/admin ClusterRoles for non-admin users (SPEC §6)
- [ ] community-operators submission once bundle is stable
- [ ] Report vmetal-openshift bug: lvms playbook fatals on missing
      operator-catalog.yaml on connected clusters (workaround: touch the file)
