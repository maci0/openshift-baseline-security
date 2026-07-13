# ubi9/go-toolset 1.26.5 (matches go.mod / Makefile GOTOOLCHAIN; bump digest with RH).
# Base digest may ship a slightly older patch; GOTOOLCHAIN pins go1.26.5 for the
# stdlib CVE floor (downloaded in the networked RUN, used offline at compile).
FROM registry.access.redhat.com/ubi9/go-toolset@sha256:c6b19c92a8613bcfdf69ef00a6ea94ac689ec76d07758d71416c1faa6f35f431 AS build
# BuildKit special-case ARG: clamps image/layer timestamps when passed by the client.
ARG SOURCE_DATE_EPOCH=0
# Pin the go.mod toolchain. Image Go may lag by a patch; never float to latest.
ENV GOTOOLCHAIN=go1.26.5
# Hermetic locale/time for any toolchain step that consults them.
# GOSUMDB must stay on so module downloads are checksum-verified (no ambient off).
# GOFLAGS=-mod=readonly: refuse silent go.mod/go.sum edits (matches Makefile).
ENV TZ=UTC LC_ALL=C SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH} GOSUMDB=sum.golang.org GOFLAGS=-mod=readonly
WORKDIR /opt/app-root/src
COPY go.mod go.sum ./
# Prefetch pinned toolchain + modules while network is available; verify sums.
# go version forces the go1.26.5 toolchain into this layer for --network=none build.
RUN go version && go mod download && go mod verify
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
# Network off after module download so the compile cannot pull new content.
# Flags match Makefile GO_BUILD_FLAGS / GO_LDFLAGS (trimpath, no VCS, stripped, empty build-id, readonly).
ENV GOPROXY=off
# --network=none: compile cannot reach the network even if GOPROXY is overridden.
# test -s: refuse an empty binary (matches Makefile build; partial linker failure).
RUN --network=none CGO_ENABLED=0 go build -trimpath -buildvcs=false -mod=readonly -ldflags="-s -w -buildid=" -o /tmp/manager ./cmd \
	&& test -s /tmp/manager

# ubi9/ubi-micro
FROM registry.access.redhat.com/ubi9/ubi-micro@sha256:35de56a9413112f1474e392ebc35e0cf6f0fb484c8e8877bbae59b513694b41f
ARG SOURCE_DATE_EPOCH=0
# Own the binary as the runtime UID (non-root cannot rewrite a root-owned path
# under a read-only rootfs anyway; keeps ownership consistent with USER).
COPY --from=build --chown=65532:65532 /tmp/manager /usr/bin/manager
USER 65532:65532
ENTRYPOINT ["/usr/bin/manager"]
