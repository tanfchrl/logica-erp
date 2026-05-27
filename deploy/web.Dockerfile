# syntax=docker/dockerfile:1.7

FROM node:22-alpine AS build
WORKDIR /src
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile || pnpm install
COPY web/ ./
RUN pnpm build

FROM caddy:2-alpine
COPY --from=build /src/dist /srv/web
COPY <<'EOF' /etc/caddy/Caddyfile
:80 {
    root * /srv/web
    encode gzip zstd
    try_files {path} /index.html
    file_server
    header /* Cache-Control "no-cache"
    header /assets/* Cache-Control "public, max-age=31536000, immutable"
}
EOF
