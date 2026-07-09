IMG ?= quay.io/openshift-baseline-security/baseline-security-operator:latest
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0

.PHONY: all
all: build

.PHONY: manifests
manifests:
	$(CONTROLLER_GEN) crd rbac:roleName=baseline-security-operator paths="./..." \
		output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac

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

.PHONY: build
build: generate manifests fmt vet
	go build -o bin/manager ./cmd

.PHONY: run
run: build
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

.PHONY: deploy
deploy: manifests
	oc apply -k config/default

.PHONY: undeploy
undeploy:
	oc delete -k config/default

BUNDLE_IMG ?= quay.io/openshift-baseline-security/baseline-security-operator-bundle:latest
CATALOG_IMG ?= quay.io/openshift-baseline-security/baseline-security-operator-catalog:latest

.PHONY: bundle
bundle: manifests verify-csv-rbac
	cp config/crd/bases/baselinesecurity.io_clusterbaselines.yaml bundle/manifests/
	docker run --rm -v $(CURDIR)/bundle:/bundle:Z quay.io/operator-framework/operator-sdk:latest bundle validate /bundle

# CSV clusterPermissions are hand-maintained; fail if they drift from role.yaml
# on verbs we know matter for zero-config (clusterbaselines create).
.PHONY: verify-csv-rbac
verify-csv-rbac:
	@grep -A1 'resources: \[clusterbaselines\]' bundle/manifests/baseline-security-operator.clusterserviceversion.yaml \
		| grep -q 'create' \
		|| (echo "CSV missing create on clusterbaselines; sync with config/rbac/role.yaml" && exit 1)

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
	docker run --rm quay.io/operator-framework/opm:latest render $(BUNDLE_IMG) --output=yaml > catalog/bundle.yaml
	printf 'schema: olm.package\nname: baseline-security-operator\ndefaultChannel: alpha\n---\nschema: olm.channel\npackage: baseline-security-operator\nname: alpha\nentries:\n  - name: baseline-security-operator.v0.1.0\n' > catalog/package.yaml
	docker run --rm -v $(CURDIR)/catalog:/catalog:Z quay.io/operator-framework/opm:latest validate /catalog
	docker build -f catalog.Dockerfile -t $(CATALOG_IMG) .
