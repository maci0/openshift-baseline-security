# Console plugin image build. Flags match operator/Makefile DOCKER_BUILD_FLAGS
# (reproducible digests: no provenance/SBOM, fixed SOURCE_DATE_EPOCH, re-pull base).
IMG ?= quay.io/openshift-baseline-security/baseline-security-console-plugin:latest
SOURCE_DATE_EPOCH ?= 0
export SOURCE_DATE_EPOCH
export DOCKER_BUILDKIT := 1
DOCKER_BUILD_FLAGS ?= --pull --provenance=false --sbom=false --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH)

.PHONY: docker-build docker-push

docker-build:
	docker build $(DOCKER_BUILD_FLAGS) -t $(IMG) .

docker-push:
	docker push $(IMG)
