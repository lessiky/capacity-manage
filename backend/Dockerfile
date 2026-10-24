# syntax=docker/dockerfile:1
# 单镜像承载 Go 服务端 + Python Worker（MVP 形态：应用节点含 Worker，见开发计划 3.4）
FROM golang:1.26-alpine AS build
WORKDIR /src/backend
ENV GOPROXY=https://goproxy.cn,direct
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN GOMAXPROCS=2 CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/capacity-server ./cmd/capacity-server

FROM python:3.11-slim
WORKDIR /app
COPY --from=build /out/capacity-server /usr/local/bin/capacity-server
COPY backend/worker /app/worker
COPY db/migrations /app/db/migrations
ENV FILE_ROOT=/app/data/files \
    DB_MIGRATION_FILE=/app/db/migrations \
    WORKER_PORT=9100 \
    WORKER_WORK_ROOT=/tmp/capworker \
    PYTHONDONTWRITEBYTECODE=1
RUN mkdir -p /app/data/files /tmp/capworker
EXPOSE 8080 9100
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=4)" || exit 1
CMD ["capacity-server", "server"]