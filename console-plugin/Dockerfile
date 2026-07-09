FROM registry.access.redhat.com/ubi9/nodejs-22:latest AS build
USER root
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN npm i -g corepack && corepack enable
WORKDIR /usr/src/app
COPY package.json yarn.lock .yarnrc.yml ./
RUN yarn install --immutable
COPY . .
RUN yarn build

# Keep the image's default nginx.conf (listen 8080, root /opt/app-root/src,
# include nginx.d/*.conf). Only add a TLS server block for the ConsolePlugin
# backend (service-ca certs mounted at /var/serving-cert).
FROM registry.access.redhat.com/ubi9/nginx-120:latest
COPY --from=build /usr/src/app/dist /opt/app-root/src
USER root
RUN printf '%s\n' \
  'server {' \
  '  listen 9443 ssl;' \
  '  listen [::]:9443 ssl;' \
  '  server_name _;' \
  '  root /opt/app-root/src;' \
  '  ssl_certificate /var/serving-cert/tls.crt;' \
  '  ssl_certificate_key /var/serving-cert/tls.key;' \
  '  ssl_session_cache shared:SSL:1m;' \
  '  ssl_session_timeout 10m;' \
  '  ssl_ciphers PROFILE=SYSTEM;' \
  '  ssl_prefer_server_ciphers on;' \
  '  include /opt/app-root/etc/nginx.default.d/*.conf;' \
  '}' \
  > /opt/app-root/etc/nginx.d/ssl.conf \
  && chown 1001:0 /opt/app-root/etc/nginx.d/ssl.conf
USER 1001
# Image entrypoint: nginx -g "daemon off;"
