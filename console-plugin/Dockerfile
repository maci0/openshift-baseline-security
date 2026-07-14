# ubi9/nodejs-22 (digest-pinned for reproducible builds; bump deliberately).
# Node in this digest is 22.23.1; keep console-plugin/.nvmrc on the same patch.
FROM registry.access.redhat.com/ubi9/nodejs-22@sha256:4068b6e9811cfdb3d833edb33f38d0d25f1c969b277040b7ca22971c2f7e6b26 AS build
# BuildKit special-case ARG: clamps image/layer timestamps when passed by the client.
ARG SOURCE_DATE_EPOCH=0
USER root
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
# Hermetic locale/time; fixed epoch for minifiers that honor SOURCE_DATE_EPOCH.
# No global Yarn cache: install must use only this layer's node_modules + lockfile.
ENV TZ=UTC LC_ALL=C SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH} YARN_ENABLE_GLOBAL_CACHE=false
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
FROM registry.access.redhat.com/ubi9/nginx-120@sha256:74a8728c8d4611de1090d09e9f8af79993ab6a2143eccd8fa14abaf7c7dc837d
ARG SOURCE_DATE_EPOCH=0
# Runtime UID owns static assets and nginx.conf (read-only rootfs still applies
# in the Deployment; ownership matches USER so the process can open its files).
COPY --from=build --chown=1001:1001 /usr/src/app/dist /opt/app-root/src
COPY --chown=1001:1001 nginx.conf /etc/nginx/nginx.conf
USER 1001
# Document the only listen port (TLS; no plaintext 8080).
EXPOSE 9443
# Graceful worker drain on pod stop (default SIGTERM is hard-stop for nginx).
STOPSIGNAL SIGQUIT
# Image entrypoint: nginx -g "daemon off;"
CMD ["nginx", "-g", "daemon off;"]
