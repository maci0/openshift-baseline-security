FROM quay.io/operator-framework/opm@sha256:e1be045e4a8558624eab2320d548b5fd557b0a0a07ebd33876b71f0778a444e4
# BuildKit special-case ARG: clamps image/layer timestamps when passed by the client.
ARG SOURCE_DATE_EPOCH=0
# Export so the opm cache RUN (and any tooling that reads the env) sees a fixed epoch.
ENV SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]
# Own configs as the runtime UID so a base-image USER drift to root cannot leave
# root-owned FBC that 1001 cannot read after we drop privileges.
COPY --chown=1001:1001 catalog /configs
# Pin non-root before cache generation so /tmp/cache is always owned by 1001
# (do not rely on the base image USER for the RUN that writes the cache).
USER 1001
# Precompute the cache; opm's runtime integrity check crash-loops without it.
RUN ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]
LABEL operators.operatorframework.io.index.configs.v1=/configs
