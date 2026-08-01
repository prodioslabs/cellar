# Build cellar-egress-gateway as a static binary, then package with iptables.
# Cross-compile on the builder's native arch ($BUILDPLATFORM); the final stage
# still needs QEMU/binfmt when targeting a foreign arch (apt-get RUN).
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
	go build -trimpath -ldflags="-s -w" -o /out/cellar-egress-gateway ./cmd/cellar-egress-gateway

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends iptables \
	&& rm -rf /var/lib/apt/lists/*
COPY --from=build /out/cellar-egress-gateway /usr/local/bin/cellar-egress-gateway
EXPOSE 53/udp 53/tcp 80/tcp 443/tcp 17948/tcp
ENTRYPOINT ["/usr/local/bin/cellar-egress-gateway", "-control-addr", "0.0.0.0:17948"]
