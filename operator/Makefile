# Release version: keep in lockstep with the CSV and console-plugin package.json.
# No OLM replaces graph is maintained (pre-release, no installed base): each
# build is a standalone channel head, so there is no PREV_VERSION.
VERSION ?= 0.5.11
IMG ?= quay.io/openshift-baseline-security/baseline-security-operator:latest
# Plugin image the operator injects via RELATED_IMAGE_CONSOLE_PLUGIN on deploy.
PLUGIN_IMG ?= quay.io/openshift-baseline-security/baseline-security-console-plugin:latest
# Match controller-tools minor to go.mod k8s.io/* (OCP 4.22 / k8s v0.35 -> CT v0.20).
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1

# Pin the Go toolchain from go.mod (single source; matches CI setup-go and the
# ubi9/go-toolset image). Override with GOTOOLCHAIN=local to use the host only.
GO_VERSION := $(shell awk '/^go /{print $$2; exit}' go.mod)
ifeq ($(GO_VERSION),)
$(error go.mod missing a `go` directive)
endif
GOTOOLCHAIN ?= go$(GO_VERSION)
export GOTOOLCHAIN

# Refuse silent go.mod/go.sum edits during test/vet/generate (matches image -mod=readonly).
# Override with GOFLAGS= when intentionally changing the module graph.
GOFLAGS ?= -mod=readonly
export GOFLAGS

# Always verify module checksums against the public sum DB. Do not inherit a
# developer shell with GOSUMDB=off (would skip integrity checks on download).
export GOSUMDB := sum.golang.org

# Fail recipes on pipe errors; use bash for pipefail portability.
SHELL := /usr/bin/env bash
.SHELLFLAGS := -eo pipefail -c

# Drop incomplete targets if a recipe fails (avoids half-written catalog/ artifacts).
.DELETE_ON_ERROR:

# Fixed epoch for local builds (matches operator/Dockerfile and plugin build).
SOURCE_DATE_EPOCH ?= 0
export SOURCE_DATE_EPOCH

# Hermetic locale/time for any toolchain step that consults them (matches Dockerfiles).
export TZ := UTC
export LC_ALL := C

# Image builds: disable BuildKit provenance/SBOM attestations so identical
# inputs produce identical image digests (attestations embed build metadata).
# --pull re-verifies digest-pinned bases (matches CI images job).
# SOURCE_DATE_EPOCH build-arg lets BuildKit clamp image/layer timestamps.
# DOCKER_BUILDKIT is required for --provenance/--sbom and SOURCE_DATE_EPOCH clamping.
export DOCKER_BUILDKIT := 1
DOCKER_BUILD_FLAGS ?= --pull --provenance=false --sbom=false --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH)

# Hardened defaults for pure filesystem validation/render docker runs.
# Pull first via ensure_image (network=none cannot pull). Host mounts are
# read-only; rootfs is read-only with a private /tmp; no capabilities retained.
DOCKER_RUN_FLAGS ?= --rm --network=none --read-only --tmpfs /tmp --cap-drop ALL --security-opt no-new-privileges:true

# Digest-pinned tool images. OPM_IMAGE is derived from catalog.Dockerfile so the
# Makefile docker-run recipes and the catalog image cannot drift (single source).
# Override with OPM_IMAGE=... only for local experiments.
OPM_IMAGE ?= $(shell sed -n 's/^FROM //p;q' catalog.Dockerfile)
OPERATOR_SDK_IMAGE ?= quay.io/operator-framework/operator-sdk@sha256:d68086836ecc9ddb28f5785815a88f5dcb33beb00e0271e3056dd13d8d352b36

# Pull a digest-pinned tool image when missing. Required before docker
# --network=none (pull cannot run with network disabled). Retries registry blips.
# Usage in a recipe line: $(call ensure_image,$(OPERATOR_SDK_IMAGE))
define ensure_image
	img="$(1)"; \
	if ! command -v docker >/dev/null 2>&1; then echo "docker is required on PATH" >&2; exit 1; fi; \
	if ! docker image inspect "$$img" >/dev/null 2>&1; then \
		pulled=0; \
		for attempt in 1 2 3; do \
			if docker pull "$$img"; then pulled=1; break; fi; \
			echo "docker pull failed (attempt $${attempt}/3): $$img" >&2; \
			sleep $$((attempt * 5)); \
		done; \
		if [ "$$pulled" -ne 1 ]; then echo "failed to pull $$img after 3 attempts" >&2; exit 1; fi; \
	fi
endef

# Escape dots so grep treats VERSION as a literal (0.4.0 must not match 0x4y0).
VERSION_RE := $(subst .,\.,$(VERSION))
GO_VERSION_RE := $(subst .,\.,$(GO_VERSION))
# Go minor (1.26 from 1.26.5) for the CI builder image tag (golang-1.26-...).
GO_MINOR := $(basename $(GO_VERSION))

.PHONY: all
all: build

.PHONY: clean
clean:
	# Whole bin/ (manager + any go run tool leftovers), not only bin/manager.
	rm -rf bin catalog

.PHONY: manifests
manifests:
	$(CONTROLLER_GEN) crd rbac:roleName=baseline-security-operator paths="./..." \
		output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
	# Keep the bundle CRD in sync so an OLM install never gets a stale schema,
	# and so CI's `make manifests && git diff --exit-code` catches bundle drift.
	cp config/crd/bases/baselinesecurity.openshift.io_clusterbaselines.yaml bundle/manifests/

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
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzNotIn$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzSetCond$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzPickComplianceOperatorCSV$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzSetComplianceOperatorReadyFromCSV$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzCheckSeverityTypeConfusion$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzPoolFromRemediationTypeConfusion$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzProfileBucketScore$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzHistoryModeMatches$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzConditionProgressing$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzRemediationOwnedByBaseline$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzOwnedSuitesRelatedObjects$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzSetRollupConditions$$ -fuzztime=5s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzNearestWaiverExpiry$$ -fuzztime=3s
	go test ./cmd/ -run=^$$ -fuzz=^FuzzMetricsCertCorruptPair$$ -fuzztime=5s
	go test ./cmd/ -run=^$$ -fuzz=^FuzzIsLoopbackMetricsAddr$$ -fuzztime=3s
	go test ./cmd/ -run=^$$ -fuzz=^FuzzValidateListenAddr$$ -fuzztime=3s
	go test ./cmd/ -run=^$$ -fuzz=^FuzzValidateMetricsCertDir$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzDashboardConfigMapCurrent$$ -fuzztime=3s
	go test ./internal/controller/ -run=^$$ -fuzz=^FuzzClampString$$ -fuzztime=3s

GOLANGCI_LINT_VERSION ?= v2.12.2
.PHONY: lint
lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

# Pin govulncheck for local and CI (single source; do not `go run ...@latest`).
GOVULNCHECK_VERSION ?= v1.6.0
.PHONY: govulncheck
govulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# Keep in lockstep with operator/Dockerfile RUN go build flags (reproducible, stripped).
# -buildid= clears the GNU build-id note so local and image binaries can match bit-for-bit.
GO_BUILD_FLAGS ?= -trimpath -buildvcs=false -mod=readonly
GO_LDFLAGS ?= -s -w -buildid=

# Codegen first as prerequisites (safe to run generate||manifests under -j).
# fmt-check/vet run in the recipe so `make -j build` cannot vet half-written
# zz_generated / CRD output from a parallel controller-gen.
.PHONY: build
build: generate manifests
	$(MAKE) --no-print-directory fmt-check
	$(MAKE) --no-print-directory vet
	# Match the image build: static, trimpath, no VCS stamp, stripped, lockfile readonly.
	CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -ldflags="$(GO_LDFLAGS)" -o bin/manager ./cmd
	# Fail if the linker wrote nothing (partial failure / empty artifact).
	@test -s bin/manager || { echo "bin/manager missing or empty after go build" >&2; exit 1; }

.PHONY: run
run: build
	# Prefer an explicit RELATED_IMAGE_CONSOLE_PLUGIN; otherwise use PLUGIN_IMG
	# (same default as make deploy) so a plain `make run` does not leave the
	# console plugin ImageMissing by accident.
	RELATED_IMAGE_CONSOLE_PLUGIN="$${RELATED_IMAGE_CONSOLE_PLUGIN:-$(PLUGIN_IMG)}" \
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
	# sed below uses | as the delimiter; reject empty or delimiter-breaking refs.
	@case "$(IMG)" in ''|*\|*) echo "invalid IMG (empty or contains |): $(IMG)" >&2; exit 1;; esac
	@case "$(PLUGIN_IMG)" in ''|*\|*) echo "invalid PLUGIN_IMG (empty or contains |): $(PLUGIN_IMG)" >&2; exit 1;; esac
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	cp -a config "$$tmp/" && \
	sed -e 's|image: quay.io/openshift-baseline-security/baseline-security-operator:.*|image: $(IMG)|' \
	    -e 's|value: quay.io/openshift-baseline-security/baseline-security-console-plugin:.*|value: $(PLUGIN_IMG)|' \
	    "$$tmp/config/manager/manager.yaml" > "$$tmp/config/manager/manager.yaml.new" && \
	mv "$$tmp/config/manager/manager.yaml.new" "$$tmp/config/manager/manager.yaml" && \
	oc apply -k "$$tmp/config/default"
	# apply -k never prunes: drop the static operator PDB a pre-0.5.7 deploy
	# left behind (removed in ADR-028; it deadlocks SNO node drains).
	oc -n openshift-baseline-security delete pdb baseline-security-operator --ignore-not-found=true

.PHONY: undeploy
undeploy:
	oc delete -k config/default --ignore-not-found=true

BUNDLE_IMG ?= quay.io/openshift-baseline-security/baseline-security-operator-bundle:latest
CATALOG_IMG ?= quay.io/openshift-baseline-security/baseline-security-operator-catalog:latest

# Fail if CSV, console-plugin package.json, catalog channel entry, README,
# CHANGELOG, and Makefile VERSION disagree. No OLM replaces graph is maintained
# (pre-release, no installed base), so there is no PREV_VERSION and no upgrade
# edge to keep in sync. Release process: bump VERSION here, the CSV
# name/version/images, console-plugin package.json (version +
# consolePlugin.version), move CHANGELOG [Unreleased] into ## [VERSION] with a
# [VERSION]: compare footer plus [Unreleased]: ...vVERSION...HEAD, set README
# "Current release", create immutable git tag vVERSION (never force-move;
# compare footers 404 without it), run REQUIRE_GIT_TAGS=1 make verify-versions
# once the tag exists, and never re-tag or overwrite a published image/CSV
# version. Do not push :VERSION images from HEAD while [Unreleased] still has
# consumer-facing entries; cut a new version first. When cutting: move
# [Unreleased] into ## [VERSION], then run RELEASE_GATE=1 REQUIRE_GIT_TAGS=1
# make verify-versions (fails if Unreleased still has bullets or the tag is missing).
# AT PUBLISH: after pushing the operator and console-plugin images, resolve each
# to its @sha256 digest and pin the CSV relatedImages, the embedded Deployment
# container image, the RELATED_IMAGE_CONSOLE_PLUGIN env, and the containerImage
# annotation to the digest form. relatedImages is what OLM mirrors for
# disconnected installs; a mutable :VERSION tag there lets a registry-side swap
# substitute a different image undetected. The :VERSION tags above are for the
# pre-publish CSV only.
.PHONY: verify-versions
verify-versions:
	@csv=bundle/manifests/baseline-security-operator.clusterserviceversion.yaml; \
	pkg=../console-plugin/package.json; \
	cl=../CHANGELOG.md; \
	readme=../README.md; \
	catpkg=catalog/package.yaml; \
	fail=0; \
	grep -qE "name: baseline-security-operator\.v$(VERSION_RE)$$" "$$csv" \
		|| { echo "CSV name is not baseline-security-operator.v$(VERSION)"; fail=1; }; \
	grep -qE '^  version: $(VERSION_RE)$$' "$$csv" \
		|| { echo "CSV spec.version is not $(VERSION)"; fail=1; }; \
	grep -qE "containerImage:.*:$(VERSION_RE)$$" "$$csv" \
		|| { echo "CSV containerImage annotation is not tagged :$(VERSION); OLM UI would show the wrong operator image"; fail=1; }; \
	grep -qE '^  "version": "$(VERSION_RE)"' "$$pkg" \
		|| { echo "console-plugin package.json top-level version is not $(VERSION); shipped plugin would report the wrong version"; fail=1; }; \
	grep -qE '^    "version": "$(VERSION_RE)"' "$$pkg" \
		|| { echo "console-plugin package.json consolePlugin.version is not $(VERSION); console would show the wrong plugin version"; fail=1; }; \
	imgs=$$(grep -oE 'quay.io/openshift-baseline-security/baseline-security-(operator|console-plugin):[^"[:space:]]+' "$$csv" || true); \
	if [ -z "$$imgs" ]; then \
		echo "CSV has no versioned operator/console-plugin image refs; release would ship without pinned images"; fail=1; \
	elif echo "$$imgs" | grep -vqE ":$(VERSION_RE)$$"; then \
		echo "CSV references an image tag that is not :$(VERSION); shipped images would disagree with the release"; fail=1; \
	fi; \
	if [ -f "$$catpkg" ]; then \
		grep -qE 'name: baseline-security-operator\.v$(VERSION_RE)$$' "$$catpkg" \
			|| { echo "catalog/package.yaml channel entry is not baseline-security-operator.v$(VERSION); FBC would advertise the wrong CSV"; fail=1; }; \
	fi; \
	grep -qE "^## \[$(VERSION_RE)\]" "$$cl" \
		|| { echo "CHANGELOG.md missing ## [$(VERSION)] section; consumers would have no release notes for this version"; fail=1; }; \
	grep -qE "^## \[Unreleased\]" "$$cl" \
		|| { echo "CHANGELOG.md missing ## [Unreleased] section; post-release work has nowhere to land"; fail=1; }; \
	unrel_line=$$(grep -nE '^## \[Unreleased\]' "$$cl" | head -1 | cut -d: -f1); \
	ver_line=$$(grep -nE "^## \[$(VERSION_RE)\]" "$$cl" | head -1 | cut -d: -f1); \
	if [ -n "$$unrel_line" ] && [ -n "$$ver_line" ] && [ "$$unrel_line" -ge "$$ver_line" ]; then \
		echo "CHANGELOG.md ## [Unreleased] must appear above ## [$(VERSION)] (Keep a Changelog order)"; fail=1; \
	fi; \
	grep -qE '"@console/pluginAPI": ">=4\.22\.0-0 <4\.23\.0-0"' "$$pkg" \
		|| { echo "console-plugin package.json @console/pluginAPI must be >=4.22.0-0 <4.23.0-0 (matches =v4.22 host pin); looser ranges would load on untested consoles"; fail=1; }; \
	grep -qE "^\[$(VERSION_RE)\]: .*compare/.*v$(VERSION_RE)$$" "$$cl" \
		|| { echo "CHANGELOG.md [$(VERSION)] footer must deep-link a compare ending at v$(VERSION)"; fail=1; }; \
	grep -qE "^\[Unreleased\]: .*compare/v$(VERSION_RE)\.\.\.HEAD" "$$cl" \
		|| { echo "CHANGELOG.md [Unreleased] footer must compare v$(VERSION)...HEAD; post-cut notes would point at the wrong base"; fail=1; }; \
	grep -qE "\*\*Current release: $(VERSION_RE)\*\*" "$$readme" \
		|| { echo "README.md Current release line is not $(VERSION); docs would advertise the wrong version"; fail=1; }; \
	grep -qE 'name: Changelog' "$$csv" \
		|| { echo "CSV links must include name: Changelog so OperatorHub consumers can find release notes"; fail=1; }; \
	grep -qE 'url: https://github.com/maci0/openshift-baseline-security/blob/.*/CHANGELOG\.md' "$$csv" \
		|| { echo "CSV Changelog link must point at the GitHub CHANGELOG.md (OLM UI has no local file)"; fail=1; }; \
	grep -qE 'Migration notes|CHANGELOG' "$$csv" \
		|| { echo "CSV description must mention Migration notes or CHANGELOG so OLM consumers see the upgrade path"; fail=1; }; \
	grep -qE "go-toolset $(GO_VERSION_RE)|go-toolset.*$(GO_VERSION_RE)" Dockerfile \
		|| { echo "Dockerfile comment must name go-toolset $(GO_VERSION) (matches go.mod); image/toolchain would drift"; fail=1; }; \
	grep -qE "GOTOOLCHAIN=go$(GO_VERSION_RE)([^0-9]|$$)" Dockerfile \
		|| { echo "release Dockerfile ENV GOTOOLCHAIN must be go$(GO_VERSION) (matches go.mod); the load-bearing toolchain pin, not just the comment, must be checked"; fail=1; }; \
	grep -qE "golang-$(GO_MINOR)-" Dockerfile.ci \
		|| { echo "Dockerfile.ci builder FROM must be golang-$(GO_MINOR)- (matches go.mod); CI toolchain would drift"; fail=1; }; \
	grep -qE "GOTOOLCHAIN=go$(GO_VERSION_RE)" Dockerfile.ci \
		|| { echo "Dockerfile.ci GOTOOLCHAIN must be go$(GO_VERSION) (matches go.mod)"; fail=1; }; \
	grep -qE "golang-$(GO_MINOR)-" ../.ci-operator.yaml \
		|| { echo ".ci-operator.yaml build_root tag must be golang-$(GO_MINOR)- (matches go.mod)"; fail=1; }; \
	grep -qE 'USER 65532:65532' Dockerfile.ci \
		|| { echo "Dockerfile.ci must run as USER 65532:65532 (parity with the release Dockerfile; CI variant is never built in GHA so lint it here)"; fail=1; }; \
	grep -qE -- '-trimpath -buildvcs=false -mod=readonly' Dockerfile.ci \
		|| { echo "Dockerfile.ci go build flags must match the release Dockerfile (reproducibility parity)"; fail=1; }; \
	nvm=$$(tr -d '[:space:]' < ../console-plugin/.nvmrc); \
	grep -qE "Node in this digest is $${nvm}" ../console-plugin/Dockerfile \
		|| { echo "console-plugin Dockerfile must document Node $${nvm} (matches .nvmrc); image/node would drift"; fail=1; }; \
	if [ "$${REQUIRE_GIT_TAGS:-}" = 1 ]; then \
		git -C .. rev-parse -q --verify "refs/tags/v$(VERSION)" >/dev/null \
			|| { echo "git tag v$(VERSION) missing; CHANGELOG compare URLs and release immutability require it (create after cutting, never force-move)"; fail=1; }; \
	fi; \
	if [ "$${RELEASE_GATE:-}" = 1 ]; then \
		if [ -n "$$unrel_line" ] && [ -n "$$ver_line" ] && \
			awk -v s="$$unrel_line" -v e="$$ver_line" 'NR>s && NR<e && /^- /{found=1} END{exit !found}' "$$cl"; then \
			echo "CHANGELOG ## [Unreleased] still has entries; move them into ## [$(VERSION)] before a release cut (RELEASE_GATE=1)"; fail=1; \
		fi; \
	fi; \
	test "$$fail" -eq 0 || exit 1

# ADR-024: operator Go vs console TS product contracts (score weights, caps,
# ProfileKey set, annotation keys). Cheap text lockstep; unit tests still cover
# behavior.
.PHONY: verify-product-lockstep
verify-product-lockstep:
	./hack/verify-product-lockstep.sh

# manifests first; static checks run in the recipe so `make -j bundle` cannot
# validate CRD/RBAC while controller-gen is still rewriting them.
.PHONY: bundle
bundle: manifests
	$(MAKE) --no-print-directory verify-crd verify-csv-rbac verify-monitoring-bundle verify-bundle-static verify-versions verify-product-lockstep
	cp config/crd/bases/baselinesecurity.openshift.io_clusterbaselines.yaml bundle/manifests/
	# DOCKER_RUN_FLAGS: pure filesystem I/O (no registry pulls, ro mount).
	# Ensure the tool image is local first (network=none cannot pull).
	@$(call ensure_image,$(OPERATOR_SDK_IMAGE))
	docker run $(DOCKER_RUN_FLAGS) -v $(CURDIR)/bundle:/bundle:ro,Z $(OPERATOR_SDK_IMAGE) bundle validate /bundle

# uniqueItems is forbidden on CRD OpenAPI schemas (kube rejects it at apply time,
# but operator-sdk bundle-validate does not); use x-kubernetes-list-type: set.
.PHONY: verify-crd
verify-crd:
	@! grep -q 'uniqueItems' config/crd/bases/*.yaml \
		|| (echo "CRD uses uniqueItems (forbidden by kube); use +listType=set instead" && exit 1)
	# complianceCatalogSource must have NO schema default: the apiserver would
	# inject it on every omitted field, so spec.complianceCatalogSource would never
	# be empty and the reconciler's OKD (community-operators) auto-detection would
	# be dead code, breaking zero-config CO install on OKD.
	@! grep -qE '^[[:space:]]*default: redhat-operators' config/crd/bases/*.yaml \
		|| (echo "CRD defaults complianceCatalogSource; drop the +kubebuilder:default so an omitted field reaches auto-detection" && exit 1)

# Unit-test the PrometheusRule alerts with promtool (containerized).
.PHONY: test-alerts
test-alerts:
	./hack/test-alerts.sh

# CSV clusterPermissions are hand-maintained and MUST stay in sync with the
# generated config/rbac/role.yaml. This is a set of targeted spot-checks (not a
# full role<->CSV diff) on the grants whose omission silently breaks an OLM
# install at runtime: any RBAC change to role.yaml must add a check here.
.PHONY: verify-csv-rbac
verify-csv-rbac:
	@grep -A1 'resources: \[clusterbaselines\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'create' \
		|| (echo "CSV missing create on clusterbaselines; sync with config/rbac/role.yaml" && exit 1)
	# operatorgroups need create;list (list detects a pre-existing user OG so we
	# defer instead of duplicating, wedging the CO install); write stays name-scoped.
	@grep -A3 'resources: \[operatorgroups\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'create, list' \
		|| (echo "CSV missing create,list on operatorgroups; OG-duplicate avoidance needs list" && exit 1)
	# machineconfigpools patch: batch remediation pauses/resumes MCPs (reboot once).
	@grep -A3 'resources: \[machineconfigpools\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'patch' \
		|| (echo "CSV missing patch on machineconfigpools; batch pause/resume needs it" && exit 1)
	# consoles update: register/deregister the dynamic plugin on the console CR.
	@grep -A3 'resources: \[consoles\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'update' \
		|| (echo "CSV missing update on consoles; console-plugin registration needs it" && exit 1)
	# infrastructures get: read status.infrastructureTopology to detect SNO and drop
	# the console-plugin PDB that would otherwise deadlock a single node's drain.
	@grep -A3 'resources: \[infrastructures\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'get' \
		|| (echo "CSV missing get on infrastructures; SNO topology detection needs it" && exit 1)
	# Name-scoped rules put resourceNames between resources and verbs; -A3 covers both
	# create-only and get/update/patch blocks. Catalog source sync needs update/patch.
	@grep -A3 'resources: \[subscriptions\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'update' \
		&& grep -A3 'resources: \[subscriptions\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'patch' \
		|| (echo "CSV missing update/patch on subscriptions; catalog source sync needs them" && exit 1)
	@grep -A3 'resources: \[subscriptions\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'resourceNames: \[compliance-operator\]' \
		|| (echo "CSV subscriptions must name-scope compliance-operator (least privilege)" && exit 1)
	@grep 'resources: \[compliancecheckresults, compliancescans, compliancesuites\]' \
		bundle/manifests/baseline-security-operator.clusterserviceversion.yaml >/dev/null \
		|| (echo "CSV missing read access to compliancesuites; completed-suite history needs it" && exit 1)

# Bundle monitoring CRs are hand-copied without namespace (OLM target NS). Fail
# if PrometheusRule or ServiceMonitor drift from config/prometheus/.
.PHONY: verify-bundle-static
verify-bundle-static:
	@./hack/verify-bundle-static.sh

.PHONY: verify-monitoring-bundle
verify-monitoring-bundle:
	@diff -u <(sed '/^  namespace: openshift-baseline-security$$/d' config/prometheus/prometheusrule.yaml) \
		bundle/manifests/baseline-security-operator_monitoring.coreos.com_v1_prometheusrule.yaml \
		|| { echo "bundle PrometheusRule drifted from config/prometheus/prometheusrule.yaml"; exit 1; }
	# Strip namespace + comments: config SM is multi-doc and annotated for humans.
	@diff -u \
		<(sed -n '/^apiVersion: monitoring.coreos.com/,$${ /^  namespace: openshift-baseline-security$$/d; /^[[:space:]]*#/d; p; }' \
			config/prometheus/servicemonitor.yaml) \
		bundle/manifests/baseline-security-operator_monitoring.coreos.com_v1_servicemonitor.yaml \
		|| { echo "bundle ServiceMonitor drifted from config/prometheus/servicemonitor.yaml"; exit 1; }

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
#
# catalog-prepare: render + validate only (no image). CI uses this so the
# catalog image can be built with buildx + GHA layer cache.
# catalog-build: prepare then docker build (local / release convenience).
# Depends on bundle so local `make catalog-build` never renders a stale/unvalidated tree.
.PHONY: catalog-prepare
catalog-prepare: bundle
	@test -n "$(BUNDLE_IMG)" || { echo "BUNDLE_IMG is empty"; exit 1; }
	@case "$(BUNDLE_IMG)" in *\|*) echo "invalid BUNDLE_IMG (contains |): $(BUNDLE_IMG)" >&2; exit 1;; esac
	@test -n "$(OPM_IMAGE)" || { echo "OPM_IMAGE is empty (catalog.Dockerfile FROM line missing?)"; exit 1; }
	@test -d bundle/manifests || { echo "bundle/manifests missing after make bundle"; exit 1; }
	rm -rf catalog
	mkdir -p catalog
	# Ensure opm is local before --network=none (cannot pull with network disabled).
	@$(call ensure_image,$(OPM_IMAGE))
	# DOCKER_RUN_FLAGS: read local bundle only; inject BUNDLE_IMG (dir render leaves image: "").
	docker run $(DOCKER_RUN_FLAGS) -v $(CURDIR)/bundle:/bundle:ro,Z $(OPM_IMAGE) \
		render /bundle --output=yaml \
		| sed 's|^image: ""$$|image: $(BUNDLE_IMG)|' > catalog/bundle.yaml
	@grep -qE '^image: .+' catalog/bundle.yaml \
		|| { echo "catalog/bundle.yaml missing image pullspec after inject"; exit 1; }
	printf 'schema: olm.package\nname: baseline-security-operator\ndefaultChannel: alpha\n---\nschema: olm.channel\npackage: baseline-security-operator\nname: alpha\nentries:\n  - name: baseline-security-operator.v$(VERSION)\n' > catalog/package.yaml
	# Clamp mtimes so COPY layers / opm cache do not embed host checkout times.
	# touch -t is portable (GNU + BSD); avoid GNU-only touch -d @epoch.
	@epoch="$(SOURCE_DATE_EPOCH)"; \
	if [ -z "$$epoch" ] || [ "$$epoch" = "0" ]; then \
		find catalog -exec touch -t 197001010000.00 {} +; \
	else \
		ts=$$(date -u -d "@$$epoch" +%Y%m%d%H%M.%S 2>/dev/null || date -u -r "$$epoch" +%Y%m%d%H%M.%S); \
		find catalog -exec touch -t "$$ts" {} +; \
	fi
	docker run $(DOCKER_RUN_FLAGS) -v $(CURDIR)/catalog:/catalog:ro,Z $(OPM_IMAGE) validate /catalog

.PHONY: catalog-build
catalog-build: catalog-prepare
	docker build $(DOCKER_BUILD_FLAGS) -f catalog.Dockerfile -t $(CATALOG_IMG) .

.PHONY: catalog-push
catalog-push:
	docker push $(CATALOG_IMG)
