# ubi9/go-toolset 1.25
FROM registry.access.redhat.com/ubi9/go-toolset@sha256:90a36bc2013b3fcb28e2a4b082c9b895d7c2c679e58b95aed9721970f3339d0e AS build
WORKDIR /opt/app-root/src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -o /tmp/manager ./cmd

# ubi9/ubi-micro
FROM registry.access.redhat.com/ubi9/ubi-micro@sha256:35de56a9413112f1474e392ebc35e0cf6f0fb484c8e8877bbae59b513694b41f
COPY --from=build /tmp/manager /usr/bin/manager
USER 65532:65532
ENTRYPOINT ["/usr/bin/manager"]
