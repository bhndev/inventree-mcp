# Must satisfy the `go 1.23.5` directive in go.mod. A 1.23.x image older than
# that patch would make the build fetch a toolchain at build time, which fails
# on a builder without network access.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/inventree-mcp ./cmd/inventree-mcp

FROM alpine:3.20
# Required: the server makes HTTPS calls to InvenTree. Without CA certificates
# those fail with x509 errors that read like network problems.
RUN apk add --no-cache ca-certificates
COPY --from=build /out/inventree-mcp /usr/local/bin/inventree-mcp
USER nobody
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/inventree-mcp"]
