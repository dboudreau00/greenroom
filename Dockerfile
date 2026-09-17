# Multi-stage build: static binary on a minimal base with the psql and
# mysql clients available for "via" routes that run inside this container.
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.Version=${VERSION}" -o /out/greenroom ./cmd/greenroom

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata postgresql16-client mysql-client openssh-client curl \
 && addgroup -S greenroom && adduser -S -G greenroom greenroom \
 && mkdir -p /workspace/environments /workspace/state \
 && chown -R greenroom:greenroom /workspace
COPY --from=build /out/greenroom /usr/local/bin/greenroom
USER greenroom
WORKDIR /workspace
ENV GREENROOM_LISTEN=0.0.0.0:8470
EXPOSE 8470
VOLUME ["/workspace/state"]
HEALTHCHECK --interval=30s --timeout=5s CMD curl -fsS http://127.0.0.1:8470/healthz || exit 1
ENTRYPOINT ["greenroom"]
CMD ["serve"]
