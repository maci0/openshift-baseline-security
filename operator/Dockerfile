# ubi9/go-toolset 1.26.6 (matches go.mod / Makefile GOTOOLCHAIN; bump digest with RH).
# Base digest may ship a slightly older patch; GOTOOLCHAIN pins go1.26.6 for the
# stdlib CVE floor (downloaded in the networked RUN, used offline at compile).
FROM registry.access.redhat.com/ubi9/go-toolset@sha256:f99dd81b20e5971ef9f63a51ac27cf0aa591ff9921d021490548b67fd9b17144 AS build
# BuildKit special-case ARG: clamps image/layer timestamps when passed by the client.
ARG SOURCE_DATE_EPOCH=0
# Pin the go.mod toolchain. Image Go may lag by a patch; never float to latest.
ENV GOTOOLCHAIN=go1.26.6
# Hermetic locale/time for any toolchain step that consults them.
# GOSUMDB must stay on so module downloads are checksum-verified (no ambient off).
# GOFLAGS=-mod=readonly: refuse silent go.mod/go.sum edits (matches Makefile).
ENV TZ=UTC LC_ALL=C SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH} GOSUMDB=sum.golang.org GOFLAGS=-mod=readonly
WORKDIR /opt/app-root/src
COPY go.mod go.sum ./
# Prefetch pinned toolchain + modules while network is available; verify sums.
# go version forces the go1.26.6 toolchain into this layer for --network=none build.
RUN go version && go mod download && go mod verify
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
# Network off after module download so the compile cannot pull new content.
# Flags match Makefile GO_BUILD_FLAGS / GO_LDFLAGS (trimpath, no VCS, stripped, empty build-id, PIE, readonly).
ENV GOPROXY=off
# --network=none: compile cannot reach the network even if GOPROXY is overridden.
# test -s: refuse an empty binary (matches Makefile build; partial linker failure).
RUN --network=none CGO_ENABLED=0 go build -trimpath -buildvcs=false -mod=readonly -buildmode=pie -ldflags="-s -w -buildid=" -o /tmp/manager ./cmd \
	&& test -s /tmp/manager

# ubi9/ubi-micro
FROM registry.access.redhat.com/ubi9/ubi-micro@sha256:f332c99eb8f798a8486821c91937f10ad64ee83d7e739303be2df051040918f6
ARG SOURCE_DATE_EPOCH=0
# Default matches Makefile VERSION; make docker-build passes --build-arg VERSION.
ARG VERSION=0.5.15
# Own the binary as the runtime UID (non-root cannot rewrite a root-owned path
# under a read-only rootfs anyway; keeps ownership consistent with USER).
# --chmod: host umask must not change the shipped layer digest.
COPY --from=build --chown=65532:65532 --chmod=0755 /tmp/manager /usr/bin/manager
# Apache-2.0 requires redistributing the license with the binary.
COPY --chmod=0644 LICENSE /licenses/LICENSE
LABEL org.opencontainers.image.title="Baseline Security Operator"
LABEL org.opencontainers.image.description="Cluster-scoped OpenShift compliance baseline operator."
LABEL org.opencontainers.image.source="https://github.com/maci0/openshift-baseline-security"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"
USER 65532:65532
ENTRYPOINT ["/usr/bin/manager"]
