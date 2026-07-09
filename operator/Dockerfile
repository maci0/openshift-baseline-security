FROM registry.access.redhat.com/ubi9/go-toolset:1.25 AS build
WORKDIR /opt/app-root/src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -o /tmp/manager ./cmd

FROM registry.access.redhat.com/ubi9/ubi-micro:latest
COPY --from=build /tmp/manager /usr/bin/manager
USER 65532:65532
ENTRYPOINT ["/usr/bin/manager"]
