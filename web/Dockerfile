# Dashboard image: static build served by nginx, with /api proxied to the Go API.
#
#   podman build -t servicemap-web web
#
# API_UPSTREAM sets where /api is proxied (default http://api:8080).

FROM docker.io/library/node:22-alpine AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY . ./
RUN npm run build -- --outDir dist

FROM docker.io/nginxinc/nginx-unprivileged:1.29-alpine
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /src/dist/ /usr/share/nginx/html/
ENV API_UPSTREAM=http://api:8080
# Exports NGINX_LOCAL_RESOLVERS from /etc/resolv.conf for the template's resolver.
ENV NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1
EXPOSE 8080
