# syntax=docker/dockerfile:1
FROM node:22-alpine AS build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --registry=https://registry.npmmirror.com
COPY frontend/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1/ >/dev/null 2>&1 || exit 1