# Builds the Things Cloud MCP server (Go) and runs it as a non-root user on a
# digest-pinned Alpine base. Hardened for self-hosting; base images are pinned
# so rebuilds are reproducible.
FROM golang:1.24-alpine@sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191 AS builder

WORKDIR /app

ENV GOTOOLCHAIN=auto

COPY . .
RUN go mod download
# Pure-Go sqlite (modernc.org/sqlite) means CGO is not needed; a static binary
# runs cleanly on the minimal runtime image and under a read-only rootfs.
RUN CGO_ENABLED=0 go build -o things-mcp .

FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

RUN apk add --no-cache ca-certificates \
 && adduser -D -H -u 65532 appuser

COPY --from=builder /app/things-mcp /usr/local/bin/things-mcp

# Default to non-root if run without an explicit runtime user. A deploy can
# override this with the runtime UID that owns the data volume.
USER 65532:65532

EXPOSE 8080

ENV PORT=8080
ENV DATA_DIR=/data

VOLUME ["/data"]

CMD ["things-mcp"]
