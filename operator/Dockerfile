# ubi9/go-toolset 1.26.6 (matches go.mod / Makefile GOTOOLCHAIN; bump digest with RH).
# Base digest may ship a slightly older patch; GOTOOLCHAIN pins go1.26.6 for the
# stdlib CVE floor (downloaded in the networked RUN, used offline at compile).
# One version literal per Dockerfile: --build-arg VERSION overrides it, the
# linker stamps it into the binary (`manager --version`), and the runtime stage
# below re-declares it for the OCI label. Every stage that reads it must say
# `ARG VERSION` so the global default reaches it; make verify-versions fails if
# this line is ever duplicated.
ARG VERSION=0.9.0
FROM registry.access.redhat.com/ubi9/go-toolset@sha256:0a4666f7a4eb0644c97a73cba198eb268691b270d97831822689e7a2088f87be AS build
# BuildKit special-case ARG: clamps image/layer timestamps when passed by the client.
ARG SOURCE_DATE_EPOCH=0
# Pulls the global VERSION above into this stage for the linker stamp.
ARG VERSION
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
# Flags match Makefile GO_BUILD_FLAGS / GO_LDFLAGS (trimpath, no VCS, stripped, empty build-id, PIE, readonly, version stamp).
ENV GOPROXY=off
# --network=none: compile cannot reach the network even if GOPROXY is overridden.
# test -s: refuse an empty binary (matches Makefile build; partial linker failure).
RUN --network=none CGO_ENABLED=0 go build -trimpath -buildvcs=false -mod=readonly -buildmode=pie -ldflags="-s -w -buildid= -X main.version=${VERSION}" -o /tmp/manager ./cmd \
	&& test -s /tmp/manager

# ubi9/ubi-micro
FROM registry.access.redhat.com/ubi9/ubi-micro@sha256:7a0454cbd9bd847e8f6a63b6f0254a6efbeb6e0ed71a5d824a4f6cccbe626650
ARG SOURCE_DATE_EPOCH=0
# The global default above; --build-arg VERSION (make docker-build) overrides it.
ARG VERSION
# Root-owned like every other file in the image: the runtime UID must not be
# able to rewrite the executable it runs. /licenses/ is copied root-owned too.
# --chmod: host umask must not change the shipped layer digest.
COPY --from=build --chown=0:0 --chmod=0755 /tmp/manager /usr/bin/manager
# Apache-2.0 requires redistributing the license with the binary.
# Two COPYs on purpose: --chmod also stamps the parent dir BuildKit creates,
# so a single 0644 copy leaves /licenses non-traversable for the non-root USER.
# Both modes are explicit so a host umask cannot move the layer digest.
COPY --chmod=0755 LICENSE /licenses/
COPY --chmod=0644 LICENSE /licenses/LICENSE
LABEL org.opencontainers.image.title="Baseline Security Operator"
LABEL org.opencontainers.image.description="Cluster-scoped OpenShift compliance baseline operator."
LABEL org.opencontainers.image.source="https://github.com/maci0/openshift-baseline-security"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"
# Documentation only (Kubernetes ignores it for reachability): the two ports the
# manager binds, so `docker inspect` on the published image does not read them as
# hidden constants. 8443 is --metrics-bind-address, scraped by Prometheus through
# the Service; 8081 is --health-probe-bind-address, called by the kubelet probes.
# Both are the defaults the Deployment passes explicitly (verify-csv-deploy holds
# the flag arguments and the probes to one number each).
EXPOSE 8443 8081
USER 65532:65532
ENTRYPOINT ["/usr/bin/manager"]
