FROM registry.access.redhat.com/ubi9/nodejs-22:latest AS build
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
FROM registry.access.redhat.com/ubi9/nginx-120:latest
COPY --from=build /usr/src/app/dist /opt/app-root/src
COPY nginx.conf /etc/nginx/nginx.conf
USER 1001
# Image entrypoint: nginx -g "daemon off;"
