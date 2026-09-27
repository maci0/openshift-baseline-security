# Contributor entry point for the repository root. Every recipe delegates to
# the real command in operator/Makefile or console-plugin/Makefile; nothing
# here re-implements a rule, so the wrapper cannot drift from what CI runs.
#
# `make ci` is the single local replica of the two per-PR GHA jobs (operator,
# console-plugin). The `images` and `catalog` jobs build and smoke-test docker
# images and have no local equivalent; they run in CI.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DELETE_ON_ERROR:

# A bare `make` prints the target list instead of starting a build.
.DEFAULT_GOAL := help

# Node major from console-plugin/.nvmrc, the same file setup-node reads in CI.
NODE_MAJOR := $(shell cut -d. -f1 console-plugin/.nvmrc)

.PHONY: help setup check test lint ci operator-test plugin-test operator-lint plugin-lint operator-ci plugin-ci

help:
	@echo "Repository contributor targets (run from the repo root):"
	@echo "  make setup        install the console-plugin dependencies, then run check"
	@echo "  make check        preflight: toolchain present, node major == $(NODE_MAJOR), plugin deps installed"
	@echo "                    (docker, shellcheck and uvx only warn: nothing in make test needs them)"
	@echo "  make test         cd operator && make test;  cd console-plugin && yarn test"
	@echo "  make lint         cd operator && make lint;  cd console-plugin && yarn lint && yarn lint:oxlint"
	@echo "  make ci           local replica of the GHA operator + console-plugin jobs (needs docker)"
	@echo "  make operator-ci  operator/Makefile ci only (unit, race, build, reproducible, lint, govulncheck, alerts, generated drift, bundle; needs docker)"
	@echo "  make plugin-ci    console-plugin yarn ci only (lint, oxlint, typecheck, test, build)"
	@echo "Not local (CI only): the images and catalog jobs. Image builds:"
	@echo "  make -C operator docker-build    make -C console-plugin docker-build"
	@echo "Per-module targets and single-test invocations:"
	@echo "  make -C operator help             make -C console-plugin help"

# One command from a clean clone to a runnable tree. The operator half needs
# nothing but Go: GOTOOLCHAIN downloads the go.mod toolchain on first use, so
# `make -C operator test` is already the install. The plugin half needs the
# project-local dependency install; the Node/Yarn versions come from
# console-plugin/.nvmrc and package.json, not from here, so the target names
# them instead of installing anything outside the tree (corepack enable writes
# to the Node prefix, which is the contributor's call to make).
.PHONY: setup
setup:
	@echo "console plugin: Node major $(NODE_MAJOR) (console-plugin/.nvmrc pins the patch), Yarn 4 via corepack:"
	@echo "  corepack enable"
	@echo "  corepack prepare \$$(node -p \"require('./console-plugin/package.json').packageManager\") --activate"
	@command -v yarn >/dev/null 2>&1 || { echo "yarn not on PATH: enable Yarn 4 with the two commands above, then re-run 'make setup'" >&2; exit 1; }
	cd console-plugin && yarn install --immutable
	@$(MAKE) --no-print-directory check

# Fail before any build, naming what is missing, instead of surfacing a
# "jest: command not found" or a toolchain download halfway through a run.
# Tools that no target in the per-clone loop needs only warn: docker, and the
# two make lint dependencies. A contributor with Go, Node and Yarn can run
# make test; failing the preflight over a linter they have not installed yet
# blocks the whole documented setup, and lint-shell / lint-python repeat the
# same message at the point of use, where it is actionable.
# The recipe is one continued shell, so no comment line may sit inside it.
check:
	@missing=0; \
	if ! command -v go >/dev/null 2>&1; then \
		echo "go not on PATH: install Go 1.21+ (the exact toolchain is pinned by the 'go' directive in operator/go.mod and downloaded on first use)" >&2; \
		missing=1; \
	fi; \
	if ! command -v node >/dev/null 2>&1; then \
		echo "node not on PATH: install Node $(NODE_MAJOR) (console-plugin/.nvmrc pins the patch, package.json engines the major)" >&2; \
		missing=1; \
	elif [ "$$(node -p 'process.versions.node.split(".")[0]')" != "$(NODE_MAJOR)" ]; then \
		echo "node $$(node -v) is not the required major $(NODE_MAJOR) (console-plugin/.nvmrc, package.json engines); every yarn script refuses to run" >&2; \
		missing=1; \
	fi; \
	if ! command -v yarn >/dev/null 2>&1; then \
		echo "yarn not on PATH: run 'corepack enable' then 'corepack prepare \$$(node -p \"require('./console-plugin/package.json').packageManager\") --activate'" >&2; \
		missing=1; \
	fi; \
	if [ ! -d console-plugin/node_modules ]; then \
		echo "console-plugin/node_modules missing: run 'cd console-plugin && yarn install --immutable'" >&2; \
		missing=1; \
	fi; \
	if ! command -v shellcheck >/dev/null 2>&1; then \
		echo "warning: shellcheck not on PATH: 'make lint' needs it (operator/hack/*.sh); brew install shellcheck / apt-get install shellcheck" >&2; \
	fi; \
	if ! command -v uvx >/dev/null 2>&1; then \
		echo "warning: uvx not on PATH: 'make lint' needs it (ruff and yamllint over operator/hack, .github, operator/config); install uv" >&2; \
	fi; \
	if ! command -v docker >/dev/null 2>&1; then \
		echo "warning: docker not on PATH: 'make ci', 'make -C operator bundle', test-alerts and the image builds need it; 'make test' and 'make lint' do not" >&2; \
	fi; \
	exit $$missing

test: operator-test plugin-test

lint: operator-lint plugin-lint

ci: operator-ci plugin-ci

operator-test:
	$(MAKE) -C operator test

plugin-test:
	$(MAKE) -C console-plugin test

operator-lint:
	$(MAKE) -C operator lint

plugin-lint:
	$(MAKE) -C console-plugin lint

operator-ci:
	$(MAKE) -C operator ci

plugin-ci:
	$(MAKE) -C console-plugin ci
