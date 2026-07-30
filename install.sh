#!/bin/sh

set -eu

REPOSITORY="prodioslabs/cellar"
VERSION="${CELLAR_VERSION:-v0.1.0}"
PREFIX="${CELLAR_PREFIX:-/usr/local}"
SYSTEMD_UNIT_DIR="${CELLAR_SYSTEMD_UNIT_DIR:-/usr/lib/systemd/system}"
SYSUSERS_DIR="${CELLAR_SYSUSERS_DIR:-/usr/lib/sysusers.d}"

fail() {
	printf 'cellar installer: %s\n' "$*" >&2
	exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

[ "$(uname -s)" = "Linux" ] || fail "only Linux is supported"

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

archive="cellar_${release_version}_linux_${arch}.tar.gz"
release_url="https://github.com/${REPOSITORY}/releases/download/${VERSION}"
tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t cellar-install)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

printf 'Downloading cellar %s for linux/%s...\n' "$VERSION" "$arch"
curl -fsSL --retry 3 -o "$tmp_dir/$archive" "$release_url/$archive"
curl -fsSL --retry 3 -o "$tmp_dir/checksums.txt" "$release_url/checksums.txt"

expected_checksum=$(
	awk -v archive="$archive" '$2 == archive || $2 == "*" archive { print $1; exit }' \
		"$tmp_dir/checksums.txt"
)
[ -n "$expected_checksum" ] || fail "checksum for $archive was not found"

if command -v sha256sum >/dev/null 2>&1; then
	actual_checksum=$(sha256sum "$tmp_dir/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
	actual_checksum=$(shasum -a 256 "$tmp_dir/$archive" | awk '{print $1}')
else
	fail "sha256sum or shasum is required"
fi

[ "$actual_checksum" = "$expected_checksum" ] || fail "checksum verification failed"

tar -xzf "$tmp_dir/$archive" -C "$tmp_dir"

for file in cellar cellard cellar-agent cellar-gateway; do
	[ -f "$tmp_dir/$file" ] || fail "$file is missing from the release archive"
done
[ -f "$tmp_dir/contrib/systemd/cellard.service" ] ||
	fail "cellard.service is missing from the release archive"
[ -f "$tmp_dir/contrib/systemd/cellar-gateway.service" ] ||
	fail "cellar-gateway.service is missing from the release archive"
[ -f "$tmp_dir/contrib/systemd/cellar.sysusers" ] ||
	fail "cellar.sysusers is missing from the release archive"

if [ "$(id -u)" -eq 0 ]; then
	sudo_cmd=""
elif command -v sudo >/dev/null 2>&1; then
	sudo_cmd="sudo"
else
	fail "run as root or install sudo"
fi

$sudo_cmd install -d "$PREFIX/bin"
for file in cellar cellard cellar-agent cellar-gateway; do
	$sudo_cmd install -m 755 "$tmp_dir/$file" "$PREFIX/bin/$file"
done

$sudo_cmd install -d "$SYSTEMD_UNIT_DIR"
$sudo_cmd install -m 644 \
	"$tmp_dir/contrib/systemd/cellard.service" \
	"$SYSTEMD_UNIT_DIR/cellard.service"
$sudo_cmd install -m 644 \
	"$tmp_dir/contrib/systemd/cellar-gateway.service" \
	"$SYSTEMD_UNIT_DIR/cellar-gateway.service"

$sudo_cmd install -d "$SYSUSERS_DIR"
$sudo_cmd install -m 644 \
	"$tmp_dir/contrib/systemd/cellar.sysusers" \
	"$SYSUSERS_DIR/cellar.conf"

printf '\nCellar %s installed successfully.\n' "$VERSION"
printf 'Run these commands to create the service user and start Cellar:\n'
printf '  sudo systemd-sysusers\n'
printf '  sudo systemctl daemon-reload\n'
printf '  sudo systemctl enable --now cellard\n'
