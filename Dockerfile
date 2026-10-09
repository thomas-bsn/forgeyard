FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /out/forgeyard-server ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/forgeyard-server /usr/local/bin/forgeyard-server
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/forgeyard-server"]
