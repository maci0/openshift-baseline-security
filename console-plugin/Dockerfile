FROM registry.access.redhat.com/ubi9/nodejs-22:latest AS build
USER root
RUN command -v yarn || npm i -g yarn
WORKDIR /usr/src/app
COPY package.json yarn.lock* .yarnrc.yml* ./
RUN yarn install --immutable || yarn install
COPY . .
RUN yarn build

FROM registry.access.redhat.com/ubi9/nginx-120:latest
COPY --from=build /usr/src/app/dist /usr/share/nginx/html
USER 1001
ENTRYPOINT ["nginx", "-g", "daemon off;"]
