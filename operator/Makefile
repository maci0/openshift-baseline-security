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
