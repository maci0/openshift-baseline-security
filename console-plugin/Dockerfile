# ubi9/nodejs-22 (digest-pinned for reproducible builds; bump deliberately)
FROM registry.access.redhat.com/ubi9/nodejs-22@sha256:c5fa408240ada5f820b6a31004fd483437ad46884505f8c6f80f650359f8add1 AS build
USER root
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN npm i -g corepack && corepack enable
WORKDIR /usr/src/app
COPY package.json yarn.lock .yarnrc.yml ./
RUN yarn install --immutable
COPY . .
RUN yarn build

# Image-equivalent layout (document root /opt/app-root/src) with TLS only on 9443.
# nginx.conf is checked into the repo (no brittle sed against the base image).
# ubi9/nginx-120
FROM registry.access.redhat.com/ubi9/nginx-120@sha256:74a8728c8d4611de1090d09e9f8af79993ab6a2143eccd8fa14abaf7c7dc837d
COPY --from=build /usr/src/app/dist /opt/app-root/src
COPY nginx.conf /etc/nginx/nginx.conf
USER 1001
# Image entrypoint: nginx -g "daemon off;"
