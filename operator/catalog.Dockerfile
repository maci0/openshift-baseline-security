FROM quay.io/operator-framework/opm@sha256:e5a6220603fb4504d58c6e3e488386b817e3695c906a62ee0370b5faedc3799a
# BuildKit special-case ARG: clamps image/layer timestamps when passed by the client.
ARG SOURCE_DATE_EPOCH=0
ARG VERSION=0.5.15
# Export so the opm cache RUN (and any tooling that reads the env) sees a fixed epoch.
ENV SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]
# Own configs as the runtime UID so a base-image USER drift to root cannot leave
# root-owned FBC that 1001 cannot read after we drop privileges.
# --chmod: host umask must not change the shipped layer digest (dirs stay traversable).
COPY --chown=1001:1001 --chmod=0755 catalog /configs
COPY --chmod=0644 LICENSE /licenses/LICENSE
# Pin non-root before cache generation so /tmp/cache is always owned by 1001
# (do not rely on the base image USER for the RUN that writes the cache).
USER 1001
# Precompute the cache; opm's runtime integrity check crash-loops without it.
# --network=none: cache is local FBC only; do not pull the bundle image at build.
RUN --network=none ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]
LABEL operators.operatorframework.io.index.configs.v1=/configs
LABEL org.opencontainers.image.title="Baseline Security Operator catalog"
LABEL org.opencontainers.image.description="File-based OLM catalog for the Baseline Security Operator."
LABEL org.opencontainers.image.source="https://github.com/maci0/openshift-baseline-security"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"
