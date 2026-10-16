# Two images from one Dockerfile:
#   docker build .                  → the server (default, last stage)
#   docker build --target agent .   → the node agent
# Build stages run on the build machine and cross-compile, so multi-arch builds need no emulation.

# --- Front ---
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- Binaries ---
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
# The commit the binaries are built from: given by CI, else read from the checkout's .git (only HEAD and
# refs are sent to the build, see .dockerignore). Agents update themselves to the server's commit.
ARG FORGEYARD_COMMIT
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN commit="$FORGEYARD_COMMIT"; \
 if [ -z "$commit" ] && [ -f .git/HEAD ]; then \
   head=$(cat .git/HEAD); \
   case "$head" in \
     "ref: "*) ref=${head#ref: }; \
       if [ -f ".git/$ref" ]; then commit=$(cat ".git/$ref"); \
       elif [ -f .git/packed-refs ]; then commit=$(grep " $ref\$" .git/packed-refs | cut -d' ' -f1); fi ;; \
     *) commit=$head ;; \
   esac; \
 fi; \
 flags="-s -w -X github.com/thomas-bsn/forgeyard/internal/version.Commit=$commit"; \
 CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="$flags" -o /out/forgeyard ./cmd/server \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="$flags" -o /out/forgeyard-agent ./cmd/agent

# --- Agent image ---
FROM alpine:3.21 AS agent
COPY --from=build /out/forgeyard-agent /usr/local/bin/
# The agent drives the host's Docker through its socket, which takes root anyway.
# Its identity (key and certificate) lives in /state: mount it as a volume.
ENV FORGEYARD_AGENT_STATE=/state
# Marks Forgeyard's own containers, which the agent never lists nor controls as external containers.
LABEL forgeyard.internal=agent
VOLUME /state
ENTRYPOINT ["forgeyard-agent"]
CMD ["run"]

# --- Server image ---
FROM alpine:3.21 AS server
RUN addgroup -S forgeyard && adduser -S -G forgeyard forgeyard \
 && mkdir -p /data /join && chown forgeyard:forgeyard /data /join
COPY --from=build /out/forgeyard /out/forgeyard-agent /usr/local/bin/
# Database, secret key and CA live in /data: mount it as a volume.
VOLUME /data
LABEL forgeyard.internal=server
USER forgeyard
# 8080: web UI and API. 8081: agents' gRPC port (mutual TLS).
EXPOSE 8080 8081
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://localhost:8080/api/health > /dev/null || exit 1
ENTRYPOINT ["forgeyard"]
CMD ["--data-dir", "/data"]
