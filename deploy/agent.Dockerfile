# syntax=docker/dockerfile:1.7

# ---- build stage ----
FROM golang:1.23-alpine AS build
WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot AS agent
WORKDIR /app
COPY --from=build /out/agent /app/agent
# Embed AGENT_CONTRACT.md files into the image so the agent can boot without
# a repo mount. internal/... is the only path that contains them today.
COPY internal /app/internal
USER nonroot
EXPOSE 8090
ENV AGENT_CONTRACTS_DIR=/app
ENTRYPOINT ["/app/agent"]
