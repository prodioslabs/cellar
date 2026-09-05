#!/bin/sh

set -eu

REPOSITORY="prodioslabs/cellar"
VERSION="${CELLAR_VERSION:-latest}"
SYSTEMD_UNIT_DIR="${CELLAR_SYSTEMD_UNIT_DIR:-/usr/lib/systemd/system}"
SYSUSERS_DIR="${CELLAR_SYSUSERS_DIR:-/usr/lib/sysusers.d}"

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

# Prefer the invoking user's home when the installer itself runs under sudo.
install_home=${HOME:-}
if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
	sudo_home=$(eval echo "~$SUDO_USER" 2>/dev/null || true)
	[ -n "$sudo_home" ] && [ "$sudo_home" != "~$SUDO_USER" ] && install_home=$sudo_home
fi
[ -n "$install_home" ] || fail "HOME is unset"
CELLAR_DATA_DIR="${CELLAR_DATA_DIR:-$install_home/.cellar}"

# Linux keeps /usr/local (typically needs sudo). macOS defaults to a
# user-writable prefix so the installer runs without sudo.
if [ -n "${CELLAR_PREFIX:-}" ]; then
	PREFIX=$CELLAR_PREFIX
elif [ "$os" = "darwin" ]; then
	PREFIX="$install_home/.local"
else
	PREFIX=/usr/local
fi

if [ "$VERSION" = "latest" ]; then
	printf 'Resolving latest cellar release...\n'
	VERSION=$(
		curl -fsSLI -o /dev/null -w '%{url_effective}' \
			"https://github.com/${REPOSITORY}/releases/latest"
	)
	VERSION=${VERSION%/}
	VERSION=${VERSION##*/}
	[ -n "$VERSION" ] || fail "could not resolve latest release tag"
fi

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
mkdir -p "$host_dir"

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
	binaries=$4

	printf 'Downloading cellar %s for %s (%s)...\n' "$VERSION" "$label" "$binaries"
	curl -fsSL --retry 3 -o "$dir/$archive" "$release_url/$archive"
	verify_archive "$archive" "$dir"
	tar -xzf "$dir/$archive" -C "$dir"
}

printf 'Fetching checksums for cellar %s...\n' "$VERSION"
curl -fsSL --retry 3 -o "$tmp_dir/checksums.txt" "$release_url/checksums.txt"

host_archive="cellar_${release_version}_${os}_${arch}.tar.gz"
download_and_extract "$host_archive" "$host_dir" "${os}/${arch}" \
	"cellar, cellard, cellar-gateway"

for file in cellar cellard cellar-gateway; do
	[ -f "$host_dir/$file" ] || fail "$file is missing from the $os release archive"
done

if [ "$os" = "linux" ]; then
	[ -f "$host_dir/contrib/systemd/cellard.service" ] ||
		fail "cellard.service is missing from the release archive"
	[ -f "$host_dir/contrib/systemd/cellar-gateway.service" ] ||
		fail "cellar-gateway.service is missing from the release archive"
	[ -f "$host_dir/contrib/systemd/cellar.sysusers" ] ||
		fail "cellar.sysusers is missing from the release archive"
elif [ "$os" = "darwin" ]; then
	[ -f "$host_dir/contrib/launchd/com.prodioslabs.cellard.plist" ] ||
		fail "com.prodioslabs.cellard.plist is missing from the release archive"
	[ -f "$host_dir/contrib/launchd/com.prodioslabs.cellar-gateway.plist" ] ||
		fail "com.prodioslabs.cellar-gateway.plist is missing from the release archive"
fi

if [ "$(id -u)" -eq 0 ]; then
	sudo_cmd=""
elif mkdir -p "$PREFIX/bin" 2>/dev/null && [ -w "$PREFIX/bin" ]; then
	sudo_cmd=""
elif command -v sudo >/dev/null 2>&1; then
	sudo_cmd="sudo"
else
	fail "run as root, install sudo, or set CELLAR_PREFIX to a writable directory"
fi

$sudo_cmd install -d "$PREFIX/bin"
for file in cellar cellard cellar-gateway; do
	$sudo_cmd install -m 755 "$host_dir/$file" "$PREFIX/bin/$file"
done

if [ "$os" = "darwin" ]; then
	install -d "$CELLAR_DATA_DIR"

	launchd_agent_dir="${CELLAR_LAUNCHD_AGENT_DIR:-$install_home/Library/LaunchAgents}"
	cellar_log_dir="${CELLAR_LOG_DIR:-$install_home/Library/Logs/cellar}"
	install -d "$launchd_agent_dir" "$cellar_log_dir"
	runtime_bindir="$PREFIX/bin"
	sed -e "s|@BINDIR@|$runtime_bindir|g" \
		-e "s|@DATA_DIR@|$CELLAR_DATA_DIR|g" \
		-e "s|@LOG_DIR@|$cellar_log_dir|g" \
		-e "s|@HOME@|$install_home|g" \
		"$host_dir/contrib/launchd/com.prodioslabs.cellard.plist" \
		> "$launchd_agent_dir/com.prodioslabs.cellard.plist"
	chmod 644 "$launchd_agent_dir/com.prodioslabs.cellard.plist"
	sed -e "s|@BINDIR@|$runtime_bindir|g" \
		-e "s|@DATA_DIR@|$CELLAR_DATA_DIR|g" \
		-e "s|@LOG_DIR@|$cellar_log_dir|g" \
		-e "s|@HOME@|$install_home|g" \
		"$host_dir/contrib/launchd/com.prodioslabs.cellar-gateway.plist" \
		> "$launchd_agent_dir/com.prodioslabs.cellar-gateway.plist"
	chmod 644 "$launchd_agent_dir/com.prodioslabs.cellar-gateway.plist"
	printf 'Installed LaunchAgents (not loaded) under %s\n' "$launchd_agent_dir"
fi

if [ "$os" = "linux" ]; then
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

printf '\nCellar %s installed successfully.\n' "$VERSION"

if [ "$os" = "linux" ]; then
	printf '\nHosts need KVM (/dev/kvm). cellard will EnsureInstalled microsandbox on first use.\n'
	printf '\nRun these commands to create the service user and start Cellar:\n'
	printf '  sudo systemd-sysusers\n'
	printf '  sudo systemctl daemon-reload\n'
	printf '  sudo systemctl enable --now cellard\n'
	printf '  sudo systemctl enable --now cellar-gateway   # optional\n'
else
	printf '\nNext steps on macOS:\n'
	printf '  1. Ensure Virtualization.framework / KVM-equivalent access is available\n'
	printf '  2. Load the daemon:   launchctl bootstrap gui/$(id -u) %s/com.prodioslabs.cellard.plist\n' \
		"${CELLAR_LAUNCHD_AGENT_DIR:-$install_home/Library/LaunchAgents}"
	printf '  3. Initialize:        cellar init --advertise-addr 127.0.0.1:17946\n'
	printf '  (Optional gateway):   launchctl bootstrap gui/$(id -u) %s/com.prodioslabs.cellar-gateway.plist\n' \
		"${CELLAR_LAUNCHD_AGENT_DIR:-$install_home/Library/LaunchAgents}"
	printf '  Data and socket live under %s.\n' "$CELLAR_DATA_DIR"
	printf '  Logs: %s\n' "${CELLAR_LOG_DIR:-$install_home/Library/Logs/cellar}"
	case ":$PATH:" in
		*"$PREFIX/bin"*) ;;
		*)
			printf '  Warning: %s/bin is not on PATH; add it so cellar/cellard are found.\n' "$PREFIX"
			;;
	esac
fi
