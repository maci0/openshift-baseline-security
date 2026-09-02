# ubi9/nodejs-22 (digest-pinned for reproducible builds; bump deliberately).
# Node in this digest is 22.23.1; keep console-plugin/.nvmrc on the same patch.
FROM registry.access.redhat.com/ubi9/nodejs-22@sha256:0905d5a102059b449b95cf3a926e073e026524e06b132b401c36c9c4f7096e37 AS build
# BuildKit special-case ARG: clamps image/layer timestamps when passed by the client.
ARG SOURCE_DATE_EPOCH=0
USER root
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
# Hermetic locale/time; fixed epoch for minifiers that honor SOURCE_DATE_EPOCH.
# No global Yarn cache: install must use only this layer's node_modules + lockfile.
# No registry lifecycle scripts: matches .yarnrc.yml enableScripts: false.
# PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: @playwright/test must not fetch browsers.
ENV TZ=UTC LC_ALL=C SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH} YARN_ENABLE_GLOBAL_CACHE=false PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 YARN_ENABLE_SCRIPTS=false
# Base image has no corepack; pin so the build does not pull floating latest from npm.
# --ignore-scripts: do not run package lifecycle scripts from the registry during bootstrap.
# --no-audit/--no-fund: install only corepack (no registry metadata side traffic).
RUN npm i -g corepack@0.34.0 --ignore-scripts --no-audit --no-fund && corepack enable
WORKDIR /usr/src/app
COPY package.json yarn.lock .yarnrc.yml ./
# packageManager field is the single source for the Yarn version (matches CI).
RUN pm="$(node -p "require('./package.json').packageManager || ''")" \
	&& test -n "$pm" \
	&& corepack prepare "$pm" --activate \
	&& yarn install --immutable
# Copy only build inputs (not tests, e2e, nginx, or local junk from context).
COPY tsconfig.json webpack.config.ts console-extensions.json ./
COPY src/ src/
COPY locales/ locales/
# --network=none: webpack must not fetch packages (install already locked them in).
RUN --network=none yarn build

# Image-equivalent layout (document root /opt/app-root/src) with TLS only on 9443.
# nginx.conf is checked into the repo (no brittle sed against the base image).
# ubi9/nginx-120
FROM registry.access.redhat.com/ubi9/nginx-120@sha256:2f3271f0fafbe12cc80843b94d2f1b36d75419f70f3f4ced69218765b106bde6
ARG SOURCE_DATE_EPOCH=0
ARG VERSION=0.5.15
# Runtime UID owns static assets and nginx.conf (read-only rootfs still applies
# in the Deployment; ownership matches USER so the process can open its files).
# --chmod: host umask must not change the shipped layer digest (dirs stay traversable).
COPY --from=build --chown=1001:1001 --chmod=0755 /usr/src/app/dist /opt/app-root/src
COPY --chown=1001:1001 --chmod=0644 nginx.conf /etc/nginx/nginx.conf
COPY --chmod=0644 LICENSE /licenses/LICENSE
LABEL org.opencontainers.image.title="Baseline Security Console Plugin"
LABEL org.opencontainers.image.description="OpenShift console plugin for baseline compliance results."
LABEL org.opencontainers.image.source="https://github.com/maci0/openshift-baseline-security"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.version="${VERSION}"
USER 1001
# Document the only listen port (TLS; no plaintext 8080).
EXPOSE 9443
# Graceful worker drain on pod stop (default SIGTERM is hard-stop for nginx).
STOPSIGNAL SIGQUIT
# Image entrypoint: nginx -g "daemon off;"
CMD ["nginx", "-g", "daemon off;"]
