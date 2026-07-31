#!/bin/sh

set -eu

REPOSITORY="prodioslabs/cellar"
VERSION="${CELLAR_VERSION:-v0.1.0}"
PREFIX="${CELLAR_PREFIX:-/usr/local}"
SYSTEMD_UNIT_DIR="${CELLAR_SYSTEMD_UNIT_DIR:-/usr/lib/systemd/system}"
SYSUSERS_DIR="${CELLAR_SYSUSERS_DIR:-/usr/lib/sysusers.d}"
SKIP_EGRESS_IMAGE="${CELLAR_SKIP_EGRESS_IMAGE:-}"
EGRESS_IMAGE="cellar/egress-gateway"

fail() {
	printf 'cellar installer: %s\n' "$*" >&2
	exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

case "$(uname -s)" in
	Linux)
		os="linux"
		;;
	Darwin)
		os="darwin"
		;;
	*)
		fail "unsupported OS: $(uname -s) (Linux and macOS only)"
		;;
esac

case "$(uname -m)" in
	x86_64 | amd64)
		arch="amd64"
		;;
	aarch64 | arm64)
		arch="arm64"
		;;
	*)
		fail "unsupported architecture: $(uname -m)"
		;;
esac

case "$VERSION" in
	v*)
		release_version=${VERSION#v}
		;;
	*)
		release_version=$VERSION
		VERSION="v$VERSION"
		;;
esac

release_url="https://github.com/${REPOSITORY}/releases/download/${VERSION}"
tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t cellar-install)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

host_dir="$tmp_dir/host"
linux_dir="$tmp_dir/linux"
mkdir -p "$host_dir" "$linux_dir"

verify_archive() {
	archive=$1
	dir=$2

	expected_checksum=$(
		awk -v archive="$archive" '$2 == archive || $2 == "*" archive { print $1; exit }' \
			"$tmp_dir/checksums.txt"
	)
	[ -n "$expected_checksum" ] || fail "checksum for $archive was not found"

	if command -v sha256sum >/dev/null 2>&1; then
		actual_checksum=$(sha256sum "$dir/$archive" | awk '{print $1}')
	elif command -v shasum >/dev/null 2>&1; then
		actual_checksum=$(shasum -a 256 "$dir/$archive" | awk '{print $1}')
	else
		fail "sha256sum or shasum is required"
	fi

	[ "$actual_checksum" = "$expected_checksum" ] ||
		fail "checksum verification failed for $archive"
}

download_and_extract() {
	archive=$1
	dir=$2
	label=$3

	printf 'Downloading cellar %s for %s...\n' "$VERSION" "$label"
	curl -fsSL --retry 3 -o "$dir/$archive" "$release_url/$archive"
	verify_archive "$archive" "$dir"
	tar -xzf "$dir/$archive" -C "$dir"
}

printf 'Fetching checksums for cellar %s...\n' "$VERSION"
curl -fsSL --retry 3 -o "$tmp_dir/checksums.txt" "$release_url/checksums.txt"

host_archive="cellar_${release_version}_${os}_${arch}.tar.gz"
download_and_extract "$host_archive" "$host_dir" "${os}/${arch}"

# On macOS, also fetch the linux archive for cellar-agent and the egress-gateway
# binary used to build the Docker image (both run inside Linux containers).
if [ "$os" = "darwin" ]; then
	linux_archive="cellar_${release_version}_linux_${arch}.tar.gz"
	download_and_extract "$linux_archive" "$linux_dir" "linux/${arch}"
	agent_src="$linux_dir/cellar-agent"
	egress_src="$linux_dir/cellar-egress-gateway"
else
	agent_src="$host_dir/cellar-agent"
	egress_src="$host_dir/cellar-egress-gateway"
fi

for file in cellar cellard cellar-gateway; do
	[ -f "$host_dir/$file" ] || fail "$file is missing from the $os release archive"
done
[ -f "$agent_src" ] || fail "cellar-agent is missing from the linux release archive"
[ -f "$egress_src" ] || fail "cellar-egress-gateway is missing from the linux release archive"

if [ "$os" = "linux" ]; then
	[ -f "$host_dir/contrib/systemd/cellard.service" ] ||
		fail "cellard.service is missing from the release archive"
	[ -f "$host_dir/contrib/systemd/cellar-gateway.service" ] ||
		fail "cellar-gateway.service is missing from the release archive"
	[ -f "$host_dir/contrib/systemd/cellar.sysusers" ] ||
		fail "cellar.sysusers is missing from the release archive"
fi

if [ "$(id -u)" -eq 0 ]; then
	sudo_cmd=""
elif command -v sudo >/dev/null 2>&1; then
	sudo_cmd="sudo"
else
	fail "run as root or install sudo"
fi

$sudo_cmd install -d "$PREFIX/bin"
for file in cellar cellard cellar-gateway; do
	$sudo_cmd install -m 755 "$host_dir/$file" "$PREFIX/bin/$file"
done
$sudo_cmd install -m 755 "$agent_src" "$PREFIX/bin/cellar-agent"

# On Linux, also install the egress-gateway binary next to cellard (parity with
# make install). On macOS the host never executes it — only the Docker image does.
if [ "$os" = "linux" ]; then
	$sudo_cmd install -m 755 "$egress_src" "$PREFIX/bin/cellar-egress-gateway"

	$sudo_cmd install -d "$SYSTEMD_UNIT_DIR"
	$sudo_cmd install -m 644 \
		"$host_dir/contrib/systemd/cellard.service" \
		"$SYSTEMD_UNIT_DIR/cellard.service"
	$sudo_cmd install -m 644 \
		"$host_dir/contrib/systemd/cellar-gateway.service" \
		"$SYSTEMD_UNIT_DIR/cellar-gateway.service"

	$sudo_cmd install -d "$SYSUSERS_DIR"
	$sudo_cmd install -m 644 \
		"$host_dir/contrib/systemd/cellar.sysusers" \
		"$SYSUSERS_DIR/cellar.conf"
fi

# Stage the linux egress binary where the Docker build context expects it.
image_ctx="$tmp_dir/image"
mkdir -p "$image_ctx"
cp "$egress_src" "$image_ctx/cellar-egress-gateway"
chmod 755 "$image_ctx/cellar-egress-gateway"

egress_image_built=0
if [ -n "$SKIP_EGRESS_IMAGE" ]; then
	printf '\nSkipping egress-gateway image build (CELLAR_SKIP_EGRESS_IMAGE is set).\n'
elif ! command -v docker >/dev/null 2>&1; then
	printf '\nDocker not found; skipping egress-gateway image build.\n'
elif ! $sudo_cmd docker info >/dev/null 2>&1; then
	printf '\nDocker daemon not reachable; skipping egress-gateway image build.\n'
else
	printf '\nBuilding %s Docker image...\n' "$EGRESS_IMAGE"
	$sudo_cmd docker build \
		-t "${EGRESS_IMAGE}:latest" \
		-t "${EGRESS_IMAGE}:${VERSION}" \
		-f - "$image_ctx" <<'EOF'
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends iptables \
	&& rm -rf /var/lib/apt/lists/*
COPY cellar-egress-gateway /usr/local/bin/cellar-egress-gateway
EXPOSE 53/udp 53/tcp 80/tcp 443/tcp 17948/tcp
ENTRYPOINT ["/usr/local/bin/cellar-egress-gateway", "-control-addr", "0.0.0.0:17948"]
EOF
	egress_image_built=1
	printf 'Tagged %s:latest and %s:%s\n' "$EGRESS_IMAGE" "$EGRESS_IMAGE" "$VERSION"
fi

printf '\nCellar %s installed successfully.\n' "$VERSION"

if [ "$egress_image_built" -eq 0 ]; then
	printf '\nNetworked sandboxes need the %s image. Build it later with:\n' "$EGRESS_IMAGE"
	printf '  curl -fsSL https://raw.githubusercontent.com/%s/main/install.sh | CELLAR_VERSION=%s sh\n' \
		"$REPOSITORY" "$VERSION"
	printf '  # or from a source checkout: make egress-gateway-image\n'
	printf '  # or load a pre-built image: docker load < egress-gateway.tar\n'
fi

if [ "$os" = "linux" ]; then
	printf '\nRun these commands to create the service user and start Cellar:\n'
	printf '  sudo systemd-sysusers\n'
	printf '  sudo systemctl daemon-reload\n'
	printf '  sudo systemctl enable --now cellard\n'
	printf '  sudo systemctl enable --now cellar-gateway   # optional\n'
else
	printf '\nNext steps on macOS:\n'
	printf '  1. Ensure Docker Desktop is running\n'
	printf '  2. Start the daemon:  sudo cellard\n'
	printf '  3. Initialize:        sudo cellar init --advertise-addr 127.0.0.1:17946\n'
	printf '  (Optional gateway):   cellar-gateway --listen 127.0.0.1:8080\n'
fi
