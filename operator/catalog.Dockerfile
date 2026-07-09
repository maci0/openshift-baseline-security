FROM quay.io/operator-framework/opm@sha256:e1be045e4a8558624eab2320d548b5fd557b0a0a07ebd33876b71f0778a444e4
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]
COPY catalog /configs
# Precompute the cache; opm's runtime integrity check crash-loops without it.
RUN ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]
LABEL operators.operatorframework.io.index.configs.v1=/configs
