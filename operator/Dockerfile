# ubi9/go-toolset 1.25.9 (matches go.mod / Makefile GOTOOLCHAIN; bump digest with RH).
FROM registry.access.redhat.com/ubi9/go-toolset@sha256:90a36bc2013b3fcb28e2a4b082c9b895d7c2c679e58b95aed9721970f3339d0e AS build
# Use only the image toolchain; never auto-download a different Go.
ENV GOTOOLCHAIN=local
WORKDIR /opt/app-root/src
COPY go.mod go.sum ./
# download then verify: refuse corrupted or swapped modules before compile.
RUN go mod download && go mod verify
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
# Network off after module download so the compile cannot pull new content.
# Flags match Makefile GO_BUILD_FLAGS / GO_LDFLAGS (trimpath, no VCS, stripped, readonly).
ENV GOPROXY=off
# Fixed epoch for any toolchain step that honors SOURCE_DATE_EPOCH (reproducible bits).
ENV SOURCE_DATE_EPOCH=0
# --network=none: compile cannot reach the network even if GOPROXY is overridden.
RUN --network=none CGO_ENABLED=0 go build -trimpath -buildvcs=false -mod=readonly -ldflags="-s -w" -o /tmp/manager ./cmd

# ubi9/ubi-micro
FROM registry.access.redhat.com/ubi9/ubi-micro@sha256:35de56a9413112f1474e392ebc35e0cf6f0fb484c8e8877bbae59b513694b41f
COPY --from=build /tmp/manager /usr/bin/manager
USER 65532:65532
ENTRYPOINT ["/usr/bin/manager"]
