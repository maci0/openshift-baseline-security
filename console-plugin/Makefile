# Console plugin image build. Flags match operator/Makefile DOCKER_BUILD_FLAGS
# (reproducible digests: no provenance/SBOM, fixed SOURCE_DATE_EPOCH, re-pull base).
# Image version label uses the Dockerfile ARG VERSION default (lockstep via
# operator `make verify-versions`); do not add a sixth VERSION source here.
IMG ?= quay.io/openshift-baseline-security/baseline-security-console-plugin:latest
SOURCE_DATE_EPOCH ?= 0
export SOURCE_DATE_EPOCH
export TZ := UTC
export LC_ALL := C
export DOCKER_BUILDKIT := 1
DOCKER_BUILD_FLAGS ?= --pull --provenance=false --sbom=false --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH)

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DELETE_ON_ERROR:

# A bare `make` must not start a docker build. Everyday commands are Yarn;
# this Makefile wraps the ones contributors look for next to operator/.
.DEFAULT_GOAL := help

.PHONY: help test lint ci docker-build docker-push

help:
	@echo "Console plugin contributor commands:"
	@echo "  yarn install --immutable"
	@echo "  make test          yarn test (jest)"
	@echo "  make lint          yarn lint && yarn lint:oxlint"
	@echo "  make ci            yarn ci (lint, oxlint, typecheck, test, production build)"
	@echo "  yarn test:watch    jest --watch"
	@echo "  yarn test src/scoring.test.ts"
	@echo "  yarn start         webpack-dev-server on :9001"
	@echo "  yarn test-e2e      Playwright (needs CONSOLE_URL; see .env.example)"
	@echo "Image:"
	@echo "  make docker-build docker-push IMG=..."

test:
	@command -v yarn >/dev/null || { echo "yarn not on PATH; enable Yarn 4 via corepack (see CONTRIBUTING.md)" >&2; exit 1; }
	yarn test

lint:
	@command -v yarn >/dev/null || { echo "yarn not on PATH; enable Yarn 4 via corepack (see CONTRIBUTING.md)" >&2; exit 1; }
	yarn lint && yarn lint:oxlint

ci:
	@command -v yarn >/dev/null || { echo "yarn not on PATH; enable Yarn 4 via corepack (see CONTRIBUTING.md)" >&2; exit 1; }
	yarn ci

docker-build:
	docker build $(DOCKER_BUILD_FLAGS) -t $(IMG) .

docker-push:
	docker push $(IMG)
