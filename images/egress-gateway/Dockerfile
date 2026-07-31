# Build cellar-egress-gateway as a static binary, then package with iptables.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cellar-egress-gateway ./cmd/cellar-egress-gateway

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends iptables \
	&& rm -rf /var/lib/apt/lists/*
COPY --from=build /out/cellar-egress-gateway /usr/local/bin/cellar-egress-gateway
# Control socket is bind-mounted at /run/cellar/egress/control.sock by cellard.
EXPOSE 53/udp 53/tcp 80/tcp 443/tcp
ENTRYPOINT ["/usr/local/bin/cellar-egress-gateway", "-control-sock", "/run/cellar/egress/control.sock"]
