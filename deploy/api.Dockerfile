# syntax=docker/dockerfile:1.7

# ---- build stage ----
FROM golang:1.23-alpine AS build
WORKDIR /src

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api    ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/logica ./cmd/logica

# ---- api runtime ----
FROM gcr.io/distroless/static-debian12:nonroot AS api
WORKDIR /app
COPY --from=build /out/api    /app/api
COPY --from=build /out/logica /app/logica
COPY migrations /app/migrations
COPY seed       /app/seed
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/app/api"]

# ---- worker runtime ----
FROM gcr.io/distroless/static-debian12:nonroot AS worker
WORKDIR /app
COPY --from=build /out/worker /app/worker
USER nonroot
ENTRYPOINT ["/app/worker"]
