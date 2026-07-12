# Release version: keep in lockstep with the CSV and console-plugin package.json.
# Bump PREV_VERSION when cutting a release that changes the OLM replaces edge.
VERSION ?= 0.4.0
PREV_VERSION ?= 0.3.1
IMG ?= quay.io/openshift-baseline-security/baseline-security-operator:latest
# Plugin image the operator injects via RELATED_IMAGE_CONSOLE_PLUGIN on deploy.
PLUGIN_IMG ?= quay.io/openshift-baseline-security/baseline-security-console-plugin:latest
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0

.PHONY: all
all: build

.PHONY: manifests
manifests:
	$(CONTROLLER_GEN) crd rbac:roleName=baseline-security-operator paths="./..." \
		output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
	# Keep the bundle CRD in sync so an OLM install never gets a stale schema,
	# and so CI's `make manifests && git diff --exit-code` catches bundle drift.
	cp config/crd/bases/baselinesecurity.io_clusterbaselines.yaml bundle/manifests/

.PHONY: generate
generate:
	$(CONTROLLER_GEN) object paths="./api/..."

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: fmt vet
	go test ./...

# Live-cluster e2e; requires KUBECONFIG pointing at a cluster with the operator
# installed (make deploy or OLM) and a ClusterBaseline reconciled.
.PHONY: test-e2e
test-e2e:
	go test -tags e2e -count=1 -timeout=20m -v ./test/e2e/...

# Short corpus-driven fuzz of pure helpers (matching, score, parsers, history).
# Patterns are anchored: -fuzz is a regexp, so FuzzScore alone also matches FuzzScore64.
.PHONY: fuzz
fuzz:
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzScore$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzScore64$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzBindingNameRoundTrip$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzProfileKeyFromSuite$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzMatchesAnyProfile$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzWithoutPlugin$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzAppendHistoryRing$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzProfileNames$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzComplianceCSVVersion$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzCompareComplianceCSVVersion$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzTailoredNameFromSuite$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzNextScanTime$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzParseScanEndTimestamp$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzCondMessage$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzSeverityWeight$$ -fuzztime=2s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzBatchRemediationNames$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzEffectiveInconsistentStatus$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzPoolFromRemediation$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzBatchPastGrace$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzClampScore$$ -fuzztime=2s

GOLANGCI_LINT_VERSION ?= v2.12.2
.PHONY: lint
lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

.PHONY: build
build: generate manifests fmt vet
	go build -trimpath -o bin/manager ./cmd

.PHONY: run
run: build
	@test -n "$$RELATED_IMAGE_CONSOLE_PLUGIN" || \
		echo "warning: RELATED_IMAGE_CONSOLE_PLUGIN is unset; console plugin will stay ImageMissing" >&2
	./bin/manager --leader-elect=false --metrics-bind-address=0

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push:
	docker push $(IMG)

.PHONY: install
install: manifests
	oc apply -f config/crd/bases

# Rewrite the kustomize base image refs so `make deploy IMG=... PLUGIN_IMG=...`
# rolls out the images just built/pushed. The checked-in base keeps :latest as
# a local default; OLM installs use versioned tags from the CSV instead.
.PHONY: deploy
deploy: manifests
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	cp -a config "$$tmp/" && \
	sed -e 's|image: quay.io/openshift-baseline-security/baseline-security-operator:.*|image: $(IMG)|' \
	    -e 's|value: quay.io/openshift-baseline-security/baseline-security-console-plugin:.*|value: $(PLUGIN_IMG)|' \
	    "$$tmp/config/manager/manager.yaml" > "$$tmp/config/manager/manager.yaml.new" && \
	mv "$$tmp/config/manager/manager.yaml.new" "$$tmp/config/manager/manager.yaml" && \
	oc apply -k "$$tmp/config/default"

.PHONY: undeploy
undeploy:
	oc delete -k config/default

BUNDLE_IMG ?= quay.io/openshift-baseline-security/baseline-security-operator-bundle:latest
CATALOG_IMG ?= quay.io/openshift-baseline-security/baseline-security-operator-catalog:latest

# Fail if CSV, console-plugin package.json, and Makefile VERSION disagree.
# Release process: bump VERSION/PREV_VERSION here, the CSV name/version/images/
# replaces, console-plugin package.json (version + consolePlugin.version), and
# CHANGELOG.md in the same change; never re-tag a published version string.
.PHONY: verify-versions
verify-versions:
	@csv=bundle/manifests/baseline-security-operator.clusterserviceversion.yaml; \
	pkg=../console-plugin/package.json; \
	fail=0; \
	grep -q "name: baseline-security-operator.v$(VERSION)$$" "$$csv" \
		|| { echo "CSV name is not baseline-security-operator.v$(VERSION)"; fail=1; }; \
	grep -qE '^  version: $(VERSION)$$' "$$csv" \
		|| { echo "CSV spec.version is not $(VERSION)"; fail=1; }; \
	grep -q "replaces: baseline-security-operator.v$(PREV_VERSION)$$" "$$csv" \
		|| { echo "CSV replaces is not baseline-security-operator.v$(PREV_VERSION)"; fail=1; }; \
	grep -oE '"version": "[^"]+"' "$$pkg" | grep -vqE "\"version\": \"$(VERSION)\"$$" \
		&& { echo "console-plugin package.json has a version (top-level or consolePlugin.version) that is not $(VERSION); shipped plugin would report the wrong version"; fail=1; } || true; \
	grep -oE 'quay.io/openshift-baseline-security/baseline-security-(operator|console-plugin):[^"[:space:]]+' "$$csv" \
		| grep -vqE ":$(VERSION)$$" \
		&& { echo "CSV references an image tag that is not :$(VERSION); shipped images would disagree with the release"; fail=1; } || true; \
	test "$$fail" -eq 0 || exit 1

.PHONY: bundle
bundle: manifests verify-crd verify-csv-rbac verify-versions
	cp config/crd/bases/baselinesecurity.io_clusterbaselines.yaml bundle/manifests/
	docker run --rm -v $(CURDIR)/bundle:/bundle:Z quay.io/operator-framework/operator-sdk@sha256:d68086836ecc9ddb28f5785815a88f5dcb33beb00e0271e3056dd13d8d352b36 bundle validate /bundle

# uniqueItems is forbidden on CRD OpenAPI schemas (kube rejects it at apply time,
# but operator-sdk bundle-validate does not); use x-kubernetes-list-type: set.
.PHONY: verify-crd
verify-crd:
	@! grep -q 'uniqueItems' config/crd/bases/*.yaml \
		|| (echo "CRD uses uniqueItems (forbidden by kube); use +listType=set instead" && exit 1)

# Unit-test the PrometheusRule alerts with promtool (containerized).
.PHONY: test-alerts
test-alerts:
	./hack/test-alerts.sh

# CSV clusterPermissions are hand-maintained; fail if they drift from role.yaml
# on verbs we know matter for zero-config (clusterbaselines create) and for
# complianceCatalogSource sync (subscriptions update/patch), or omit a watched
# Compliance API required for history aggregation.
.PHONY: verify-csv-rbac
verify-csv-rbac:
	@grep -A1 'resources: \[clusterbaselines\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'create' \
		|| (echo "CSV missing create on clusterbaselines; sync with config/rbac/role.yaml" && exit 1)
	@grep -A1 'resources: \[subscriptions\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'update' \
		&& grep -A1 'resources: \[subscriptions\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'patch' \
		|| (echo "CSV missing update/patch on subscriptions; catalog source sync needs them" && exit 1)
	@grep 'resources: \[compliancecheckresults, compliancescans, compliancesuites\]' \
		bundle/manifests/baseline-security-operator.clusterserviceversion.yaml >/dev/null \
		|| (echo "CSV missing read access to compliancesuites; completed-suite history needs it" && exit 1)

.PHONY: bundle-build
bundle-build: bundle
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push:
	docker push $(BUNDLE_IMG)

# File-based catalog containing just this package; requires BUNDLE_IMG pushed.
.PHONY: catalog-build
catalog-build:
	mkdir -p catalog
	docker run --rm quay.io/operator-framework/opm@sha256:e1be045e4a8558624eab2320d548b5fd557b0a0a07ebd33876b71f0778a444e4 render $(BUNDLE_IMG) --output=yaml > catalog/bundle.yaml
	printf 'schema: olm.package\nname: baseline-security-operator\ndefaultChannel: alpha\n---\nschema: olm.channel\npackage: baseline-security-operator\nname: alpha\nentries:\n  - name: baseline-security-operator.v$(VERSION)\n    replaces: baseline-security-operator.v$(PREV_VERSION)\n' > catalog/package.yaml
	docker run --rm -v $(CURDIR)/catalog:/catalog:Z quay.io/operator-framework/opm@sha256:e1be045e4a8558624eab2320d548b5fd557b0a0a07ebd33876b71f0778a444e4 validate /catalog
	docker build -f catalog.Dockerfile -t $(CATALOG_IMG) .
