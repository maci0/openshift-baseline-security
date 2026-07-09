FROM quay.io/operator-framework/opm:latest
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]
COPY catalog /configs
LABEL operators.operatorframework.io.index.configs.v1=/configs
