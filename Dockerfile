# --- Front ---
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- Binaries ---
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/forgeyard ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/forgeyard-agent ./cmd/agent

# --- Image ---
FROM alpine:3.21
RUN addgroup -S forgeyard && adduser -S -G forgeyard forgeyard \
 && mkdir -p /data && chown forgeyard:forgeyard /data
COPY --from=build /out/forgeyard /out/forgeyard-agent /usr/local/bin/
# Database, secret key and CA live in /data: mount it as a volume.
VOLUME /data
USER forgeyard
# 8080: web UI and API. 8081: agents' gRPC port (mutual TLS).
EXPOSE 8080 8081
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://localhost:8080/api/health > /dev/null || exit 1
ENTRYPOINT ["forgeyard"]
CMD ["--data-dir", "/data"]
