# Console plugin image build. Flags match operator/Makefile DOCKER_BUILD_FLAGS
# (reproducible digests: no provenance/SBOM, fixed SOURCE_DATE_EPOCH, re-pull base).
IMG ?= quay.io/openshift-baseline-security/baseline-security-console-plugin:latest
# Image version label, read from package.json: one of the five lockstep files
# `operator make verify-versions` already checks, so this is a read, not a sixth
# hand-maintained source. The Dockerfile ARG VERSION default stays the fallback
# for a plain `docker build`, and verify-versions pins that default too.
VERSION ?= $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' package.json | head -1)
SOURCE_DATE_EPOCH ?= 0
export SOURCE_DATE_EPOCH
export TZ := UTC
export LC_ALL := C
export DOCKER_BUILDKIT := 1
DOCKER_BUILD_FLAGS ?= --pull --provenance=false --sbom=false --build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) --build-arg VERSION=$(VERSION)

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DELETE_ON_ERROR:

# A bare `make` must not start a docker build. Everyday commands are bun;
# this Makefile wraps the ones contributors look for next to operator/.
.DEFAULT_GOAL := help

.PHONY: help test lint size ci docker-build docker-push

help:
	@echo "Console plugin contributor commands:"
	@echo "  bun install --frozen-lockfile"
	@echo "  make test              bun run test (jest)"
	@echo "  make lint              bun run lint && bun run lint:oxlint"
	@echo "  make ci                bun run ci (lint, oxlint, typecheck, test, production build)"
	@echo "  bun run test:watch     jest --watch"
	@echo "  bun run test src/scoring.test.ts"
	@echo "  bun run start          webpack-dev-server on :9001 (console origin:"
	@echo "                         PLUGIN_DEV_ALLOWED_ORIGIN, default http://localhost:3000)"
	@echo "  bun run test-e2e       Playwright (needs CONSOLE_URL; see .env.example)"
	@echo "                         plus a one-time: bunx playwright install chromium"
	@echo "  make size              bun run size (transferred bytes and the budget gate)"
	@echo "  bun run licenses       regenerate dist/THIRD-PARTY-NOTICES.txt (also a build step)"
	@echo "Image:"
	@echo "  make docker-build docker-push IMG=... [VERSION=x.y.z]"

test:
	@command -v bun >/dev/null || { echo "bun not on PATH; install the bun version packageManager in package.json pins (see CONTRIBUTING.md)" >&2; exit 1; }
	bun run test

lint:
	@command -v bun >/dev/null || { echo "bun not on PATH; install the bun version packageManager in package.json pins (see CONTRIBUTING.md)" >&2; exit 1; }
	bun run lint && bun run lint:oxlint

size:
	@command -v bun >/dev/null || { echo "bun not on PATH; install the bun version packageManager in package.json pins (see CONTRIBUTING.md)" >&2; exit 1; }
	bun run size

ci:
	@command -v bun >/dev/null || { echo "bun not on PATH; install the bun version packageManager in package.json pins (see CONTRIBUTING.md)" >&2; exit 1; }
	bun run ci

docker-build:
	# An unreadable or empty version would stamp org.opencontainers.image.version
	# with an empty string instead of failing; fail before the (slow) build.
	@test -n "$(VERSION)" || { echo "cannot read VERSION from console-plugin/package.json; set VERSION=<x.y.z>" >&2; exit 1; }
	docker build $(DOCKER_BUILD_FLAGS) -t $(IMG) .

docker-push:
	docker push $(IMG)
