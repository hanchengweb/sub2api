# =============================================================================
# Sub2API Multi-Stage Dockerfile
# =============================================================================
# Stage 1: Build frontend
# Stage 2: Build Go backend with embedded frontend
# Stage 3: Final minimal image
# =============================================================================

ARG NODE_IMAGE=node:24-alpine
ARG GOLANG_IMAGE=golang:1.26.5-alpine
ARG ALPINE_IMAGE=alpine:3.20
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn

# -----------------------------------------------------------------------------
# Stage 1: Frontend Builder
# -----------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS frontend-builder
# npm 源。留空走官方源；发布脚本传国内镜像，见下面装依赖那一步。
ARG NPM_REGISTRY=

WORKDIR /app/frontend
# vue-tsc + vite build 的峰值堆已超过 1536MB：2026-09-07 在 14GB 内存的构建机上
# 实测 V8 在 ~1530MB 触顶后 FATAL ERROR: Ineffective mark-compacts near heap limit
# 而中止（SIGABRT / exit 134），整个前端阶段无法完成。这是 V8 的堆上限而非机器
# 内存不足，故直接抬高上限。根 Dockerfile 不设此变量、用 Node 默认值，所以只有
# 这个经典 builder 路径会撞上。
ENV NODE_OPTIONS=--max-old-space-size=4096

# Install pnpm. Keep this pinned to the lockfile-compatible major version so
# Docker builds remain reproducible when pnpm changes config validation rules.
# corepack 下载 pnpm 本身也走 npm 源，一并换。
RUN corepack enable \
    && if [ -n "$NPM_REGISTRY" ]; then \
         COREPACK_NPM_REGISTRY="$NPM_REGISTRY" corepack prepare pnpm@9.15.9 --activate \
         || corepack prepare pnpm@9.15.9 --activate; \
       else \
         corepack prepare pnpm@9.15.9 --activate; \
       fi

# Install dependencies first (better caching)
# 依赖换国内源。这层缓存每次发布成功后都会被清悬空镜像一并删掉（旧版构建器的缓存
# 就是中间镜像），所以每次发布都是冷装。生产机在杭州，官方源走跨境线路：2026-09-27
# 一次发布在这一步卡了 30 多分钟（实测 12~108 KB/s），同一台机器上 npmmirror 8.7 MB/s。
# lockfile 里的包都带 sha512 完整性校验、没有写死下载地址，换源不改变装到的内容——
# 镜像站给错包会校验失败，不会被装进去。镜像站故障时退回官方源重装，不让换源成为新的单点。
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN if [ -n "$NPM_REGISTRY" ]; then \
      (pnpm config set registry "$NPM_REGISTRY" && pnpm install --frozen-lockfile) \
      || (pnpm config delete registry; pnpm install --frozen-lockfile); \
    else \
      pnpm install --frozen-lockfile; \
    fi

# Copy frontend source and build.
# LegalDocumentView.vue (admin-compliance gate) build-time imports
# ../../../../docs/legal/*.md?raw, so docs/legal/ must sit beside frontend/
# in the image (WORKDIR /app/frontend -> resolves to /app/docs/legal/*.md).
# Copy only that subtree to keep the build dependency minimal.
COPY frontend/ ./
COPY docs/legal/ /app/docs/legal/
RUN pnpm run build

# -----------------------------------------------------------------------------
# Stage 2: Backend Builder
# -----------------------------------------------------------------------------
FROM ${GOLANG_IMAGE} AS backend-builder

# Build arguments for version info (set by CI)
ARG VERSION=
ARG COMMIT=docker
ARG DATE
ARG GOPROXY
ARG GOSUMDB

ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

# Install build dependencies
# apk 换国内源。生产机在杭州，dl-cdn.alpinelinux.org 时快时慢：2026-09-11 一次
# 发布里光这两步 apk 就从几十秒变成 25 分钟，把发布拖到接近轮询上限。
# 镜像站临时故障时还原默认源重试，不让换源本身成为新的单点。
RUN cp /etc/apk/repositories /etc/apk/repositories.bak \
    && sed -i 's#https://dl-cdn.alpinelinux.org#https://mirrors.aliyun.com#g' /etc/apk/repositories \
    && (apk add --no-cache git ca-certificates tzdata \
        || (cp /etc/apk/repositories.bak /etc/apk/repositories \
            && apk add --no-cache git ca-certificates tzdata))

WORKDIR /app/backend

# Copy go mod files first (better caching)
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy backend source first
COPY backend/ ./

# Copy frontend dist from previous stage (must be after backend copy to avoid being overwritten)
COPY --from=frontend-builder /app/backend/internal/web/dist ./internal/web/dist

# Build the binary (BuildType=release for CI builds, embed frontend)
# Version precedence: build arg VERSION > exact git tag > cmd/server/VERSION
RUN VERSION_VALUE="${VERSION}" && \
    if [ -z "${VERSION_VALUE}" ]; then VERSION_VALUE="$(./scripts/resolve-version.sh)"; fi && \
    DATE_VALUE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" && \
    CGO_ENABLED=0 GOOS=linux go build \
    -tags embed \
    -ldflags="-s -w -X main.Version=${VERSION_VALUE} -X main.Commit=${COMMIT} -X main.Date=${DATE_VALUE} -X main.BuildType=release" \
    -o /app/sub2api \
    ./cmd/server

# -----------------------------------------------------------------------------
# Stage 3: Final Runtime Image
# -----------------------------------------------------------------------------
FROM ${ALPINE_IMAGE}

# Labels
LABEL maintainer="Wei-Shaw <github.com/Wei-Shaw>"
LABEL description="Sub2API - AI API Gateway Platform"
LABEL org.opencontainers.image.source="https://github.com/hanchengweb/sub2api"

# Install runtime dependencies
# apk 换国内源。生产机在杭州，dl-cdn.alpinelinux.org 时快时慢：2026-09-11 一次
# 发布里光这两步 apk 就从几十秒变成 25 分钟，把发布拖到接近轮询上限。
# 镜像站临时故障时还原默认源重试，不让换源本身成为新的单点。
RUN cp /etc/apk/repositories /etc/apk/repositories.bak \
    && sed -i 's#https://dl-cdn.alpinelinux.org#https://mirrors.aliyun.com#g' /etc/apk/repositories \
    && (apk add --no-cache ca-certificates tzdata curl su-exec \
        || (cp /etc/apk/repositories.bak /etc/apk/repositories \
            && apk add --no-cache ca-certificates tzdata curl su-exec)) \
    && rm -rf /var/cache/apk/*

# Create non-root user
RUN addgroup -g 1000 sub2api && \
    adduser -u 1000 -G sub2api -s /bin/sh -D sub2api

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=backend-builder /app/sub2api /app/sub2api

# Create data directory
RUN mkdir -p /app/data && chown -R sub2api:sub2api /app

# Copy entrypoint script (fixes volume permissions then drops to sub2api)
COPY deploy/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# Expose port (can be overridden by SERVER_PORT env var)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget -q -T 5 -O /dev/null http://localhost:${SERVER_PORT:-8080}/health || exit 1

# Run the application (entrypoint fixes /app/data ownership then execs as sub2api)
ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/sub2api"]
