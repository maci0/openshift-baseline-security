# Release version: keep in lockstep with the CSV and console-plugin package.json.
# Bump PREV_VERSION when cutting a release that changes the OLM replaces edge.
VERSION ?= 0.4.0
PREV_VERSION ?= 0.3.1
IMG ?= quay.io/openshift-baseline-security/baseline-security-operator:latest
# Plugin image the operator injects via RELATED_IMAGE_CONSOLE_PLUGIN on deploy.
PLUGIN_IMG ?= quay.io/openshift-baseline-security/baseline-security-console-plugin:latest
# Match controller-tools minor to go.mod k8s.io/* (v0.35 -> CT v0.20 per VERSIONING.md).
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1

# Pin the Go toolchain to the go.mod `go` line so local `make` matches CI/setup-go
# and does not silently use a newer host compiler (GOTOOLCHAIN=local to override).
# Keep in lockstep with go.mod and the ubi9/go-toolset image (currently 1.25.9).
GOTOOLCHAIN ?= go1.25.9
export GOTOOLCHAIN

# Refuse silent go.mod/go.sum edits during test/vet/generate (matches image -mod=readonly).
# Override with GOFLAGS= when intentionally changing the module graph.
GOFLAGS ?= -mod=readonly
export GOFLAGS

# Fail recipes on pipe errors; use bash for pipefail portability.
SHELL := /usr/bin/env bash
.SHELLFLAGS := -eo pipefail -c

# Drop incomplete targets if a recipe fails (avoids half-written catalog/ artifacts).
.DELETE_ON_ERROR:

# Image builds: disable BuildKit provenance/SBOM attestations so identical
# inputs produce identical image digests (attestations embed build metadata).
# --pull re-verifies digest-pinned bases (matches CI images job).
DOCKER_BUILD_FLAGS ?= --pull --provenance=false --sbom=false

# Fixed epoch for local builds (matches operator/Dockerfile and plugin build).
SOURCE_DATE_EPOCH ?= 0
export SOURCE_DATE_EPOCH

# Digest-pinned tool images (single source for Makefile docker run recipes).
# Keep digests in lockstep with catalog.Dockerfile (opm) when bumping.
OPM_IMAGE ?= quay.io/operator-framework/opm@sha256:e1be045e4a8558624eab2320d548b5fd557b0a0a07ebd33876b71f0778a444e4
OPERATOR_SDK_IMAGE ?= quay.io/operator-framework/operator-sdk@sha256:d68086836ecc9ddb28f5785815a88f5dcb33beb00e0271e3056dd13d8d352b36

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

# Check-only: do not rewrite the tree (test/build must stay non-mutating).
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:" >&2; \
		echo "$$unformatted" >&2; \
		echo "run: make fmt" >&2; \
		exit 1; \
	fi

# Integrity of the module cache against go.sum (same gate as the operator image).
.PHONY: mod-verify
mod-verify:
	go mod download
	go mod verify

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: fmt-check vet mod-verify
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
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzClampFailureList$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzNormalizedSchedule$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzSyncFailureDiff$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzSyncHistorySnapshot$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzValidMCPPoolName$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzValidRelatedImage$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzSplitCSV$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzClampHistory$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzCheckSeverity$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzSanitizeStatusForUpdate$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzCompletedSuiteTimes$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzCompletedSuiteTimesTypeConfusion$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzUnstructuredMetadataReads$$ -fuzztime=3s
	go test ./cmd/ -run=^$$ -fuzz=^FuzzMetricsCertCorruptPair$$ -fuzztime=5s
	go test ./cmd/ -run=^$$ -fuzz=^FuzzIsLoopbackMetricsAddr$$ -fuzztime=3s

GOLANGCI_LINT_VERSION ?= v2.12.2
.PHONY: lint
lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

# Keep in lockstep with operator/Dockerfile RUN go build flags (reproducible, stripped).
GO_BUILD_FLAGS ?= -trimpath -buildvcs=false -mod=readonly
GO_LDFLAGS ?= -s -w

.PHONY: build
build: generate manifests fmt-check vet
	# Match the image build: static, trimpath, no VCS stamp, stripped, lockfile readonly.
	CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/manager ./cmd

.PHONY: run
run: build
	@test -n "$$RELATED_IMAGE_CONSOLE_PLUGIN" || \
		echo "warning: RELATED_IMAGE_CONSOLE_PLUGIN is unset; console plugin will stay ImageMissing" >&2
	./bin/manager --leader-elect=false --metrics-bind-address=0

.PHONY: docker-build
docker-build:
	docker build $(DOCKER_BUILD_FLAGS) -t $(IMG) .

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

# Fail if CSV, console-plugin package.json, README, CHANGELOG, and Makefile
# VERSION disagree. Release process: bump VERSION/PREV_VERSION here, the CSV
# name/version/images/replaces, console-plugin package.json (version +
# consolePlugin.version), move CHANGELOG [Unreleased] into ## [VERSION] with
# a [VERSION]: compare footer and [Unreleased]: ...vVERSION...HEAD, set README
# "Current release" and upgrade path, create immutable git tag vVERSION (never
# force-move), and never re-tag or overwrite a published image/CSV version.
.PHONY: verify-versions
verify-versions:
	@csv=bundle/manifests/baseline-security-operator.clusterserviceversion.yaml; \
	pkg=../console-plugin/package.json; \
	cl=../CHANGELOG.md; \
	readme=../README.md; \
	fail=0; \
	grep -q "name: baseline-security-operator.v$(VERSION)$$" "$$csv" \
		|| { echo "CSV name is not baseline-security-operator.v$(VERSION)"; fail=1; }; \
	grep -qE '^  version: $(VERSION)$$' "$$csv" \
		|| { echo "CSV spec.version is not $(VERSION)"; fail=1; }; \
	grep -q "replaces: baseline-security-operator.v$(PREV_VERSION)$$" "$$csv" \
		|| { echo "CSV replaces is not baseline-security-operator.v$(PREV_VERSION)"; fail=1; }; \
	grep -qE "containerImage:.*:$(VERSION)$$" "$$csv" \
		|| { echo "CSV containerImage annotation is not tagged :$(VERSION); OLM UI would show the wrong operator image"; fail=1; }; \
	grep -oE '"version": "[^"]+"' "$$pkg" | grep -vqE "\"version\": \"$(VERSION)\"$$" \
		&& { echo "console-plugin package.json has a version (top-level or consolePlugin.version) that is not $(VERSION); shipped plugin would report the wrong version"; fail=1; } || true; \
	imgs=$$(grep -oE 'quay.io/openshift-baseline-security/baseline-security-(operator|console-plugin):[^"[:space:]]+' "$$csv" || true); \
	if [ -z "$$imgs" ]; then \
		echo "CSV has no versioned operator/console-plugin image refs; release would ship without pinned images"; fail=1; \
	elif echo "$$imgs" | grep -vqE ":$(VERSION)$$"; then \
		echo "CSV references an image tag that is not :$(VERSION); shipped images would disagree with the release"; fail=1; \
	fi; \
	grep -qE "^## \[$(VERSION)\]" "$$cl" \
		|| { echo "CHANGELOG.md missing ## [$(VERSION)] section; consumers would have no release notes for this version"; fail=1; }; \
	grep -qE "^## \[$(PREV_VERSION)\]" "$$cl" \
		|| { echo "CHANGELOG.md missing ## [$(PREV_VERSION)] section (PREV_VERSION); replaces edge has no prior notes"; fail=1; }; \
	grep -qE "^## \[Unreleased\]" "$$cl" \
		|| { echo "CHANGELOG.md missing ## [Unreleased] section; post-release work has nowhere to land"; fail=1; }; \
	unrel_line=$$(grep -nE '^## \[Unreleased\]' "$$cl" | head -1 | cut -d: -f1); \
	ver_line=$$(grep -nE "^## \[$(VERSION)\]" "$$cl" | head -1 | cut -d: -f1); \
	if [ -n "$$unrel_line" ] && [ -n "$$ver_line" ] && [ "$$unrel_line" -ge "$$ver_line" ]; then \
		echo "CHANGELOG.md ## [Unreleased] must appear above ## [$(VERSION)] (Keep a Changelog order)"; fail=1; \
	fi; \
	grep -qE "^\[$(VERSION)\]:" "$$cl" \
		|| { echo "CHANGELOG.md missing [$(VERSION)]: compare footer link; release notes would not deep-link the delta"; fail=1; }; \
	grep -qE "^\[Unreleased\]: .*compare/v$(VERSION)\.\.\.HEAD" "$$cl" \
		|| { echo "CHANGELOG.md [Unreleased] footer must compare v$(VERSION)...HEAD; post-cut notes would point at the wrong base"; fail=1; }; \
	grep -qE "\*\*Current release: $(VERSION)\*\*" "$$readme" \
		|| { echo "README.md Current release line is not $(VERSION); docs would advertise the wrong version"; fail=1; }; \
	grep -qE "→ $(VERSION)" "$$readme" \
		|| { echo "README.md upgrade path does not end at → $(VERSION); consumers would not see the OLM replaces chain end"; fail=1; }; \
	test "$$fail" -eq 0 || exit 1

.PHONY: bundle
bundle: manifests verify-crd verify-csv-rbac verify-monitoring-bundle verify-versions
	cp config/crd/bases/baselinesecurity.io_clusterbaselines.yaml bundle/manifests/
	# --network=none: validate is pure filesystem I/O (no registry pulls).
	docker run --rm --network=none -v $(CURDIR)/bundle:/bundle:Z $(OPERATOR_SDK_IMAGE) bundle validate /bundle

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

# Bundle PrometheusRule is hand-copied without namespace (OLM target NS). Fail
# if alert expressions drift from config/prometheus/prometheusrule.yaml.
.PHONY: verify-monitoring-bundle
verify-monitoring-bundle:
	@diff -u <(sed '/^  namespace: openshift-baseline-security$$/d' config/prometheus/prometheusrule.yaml) \
		bundle/manifests/baseline-security-operator_monitoring.coreos.com_v1_prometheusrule.yaml \
		|| { echo "bundle PrometheusRule drifted from config/prometheus/prometheusrule.yaml"; exit 1; }

.PHONY: bundle-build
bundle-build: bundle
	docker build $(DOCKER_BUILD_FLAGS) -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: bundle-push
bundle-push:
	docker push $(BUNDLE_IMG)

# File-based catalog for this package. BUNDLE_IMG is the pullspec OLM installs
# from (must be pushable/pullable at install time; local tags are fine for CI
# schema validation). Render from the on-disk bundle/ (already validated by
# `make bundle`) so catalog content cannot drift from a mutable registry tag
# and so builds work offline without docker.sock / a pushed bundle image.
# Wipe catalog/ first so stale YAML from a prior build cannot ship in the image.
.PHONY: catalog-build
catalog-build:
	@test -n "$(BUNDLE_IMG)" || { echo "BUNDLE_IMG is empty"; exit 1; }
	@test -d bundle/manifests || { echo "bundle/manifests missing; run make bundle first"; exit 1; }
	rm -rf catalog
	mkdir -p catalog
	# --network=none: read local bundle only; inject BUNDLE_IMG (dir render leaves image: "").
	docker run --rm --network=none -v $(CURDIR)/bundle:/bundle:ro,Z $(OPM_IMAGE) \
		render /bundle --output=yaml \
		| sed 's|^image: ""$$|image: $(BUNDLE_IMG)|' > catalog/bundle.yaml
	@grep -qE '^image: .+' catalog/bundle.yaml \
		|| { echo "catalog/bundle.yaml missing image pullspec after inject"; exit 1; }
	printf 'schema: olm.package\nname: baseline-security-operator\ndefaultChannel: alpha\n---\nschema: olm.channel\npackage: baseline-security-operator\nname: alpha\nentries:\n  - name: baseline-security-operator.v$(VERSION)\n    replaces: baseline-security-operator.v$(PREV_VERSION)\n' > catalog/package.yaml
	docker run --rm --network=none -v $(CURDIR)/catalog:/catalog:Z $(OPM_IMAGE) validate /catalog
	docker build $(DOCKER_BUILD_FLAGS) -f catalog.Dockerfile -t $(CATALOG_IMG) .

.PHONY: catalog-push
catalog-push:
	docker push $(CATALOG_IMG)
