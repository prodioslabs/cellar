#!/usr/bin/env bash

# Must run before bash-only `set -o pipefail` so `curl … | sh` fails clearly.
[ -n "${BASH_VERSION:-}" ] || {
	echo "cellar installer: run with bash (e.g. curl … | bash)" >&2
	exit 1
}

set -euo pipefail

REPOSITORY="prodioslabs/cellar"
VERSION="${CELLAR_VERSION:-latest}"
SYSTEMD_UNIT_DIR="${CELLAR_SYSTEMD_UNIT_DIR:-/usr/lib/systemd/system}"
SYSUSERS_DIR="${CELLAR_SYSUSERS_DIR:-/usr/lib/sysusers.d}"

# Valid component names (space-separated).
ALL_COMPONENTS="cellar cellard cellar-gateway"
# Manager-friendly defaults: CLI + daemon, no gateway.
DEFAULT_COMPONENTS="cellar cellard"

fail() {
	printf 'cellar installer: %s\n' "$*" >&2
	exit 1
}

# ./install.sh | INSTALL_CELLAR_GATEWAY=true closes the pipe immediately; the next
# printf to stdout then dies with SIGPIPE and no message under set -e.
trap 'fail "stdout pipe closed early. Do not pipe into INSTALL_CELLAR_GATEWAY=…. Use: INSTALL_CELLAR_GATEWAY=true '"$0"'   or: '"$0"' --install-cellar-gateway true"' PIPE

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

# microsandbox local runtime is Apple Silicon only (Intel Macs and Rosetta are not supported).
# https://docs.microsandbox.dev/troubleshooting/macos
if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ]; then
	fail "macOS requires Apple Silicon (arm64); Intel Macs are not supported"
fi

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

is_valid_component() {
	case " $ALL_COMPONENTS " in
		*" $1 "*) return 0 ;;
		*) return 1 ;;
	esac
}

component_selected() {
	case " $selected_components " in
		*" $1 "*) return 0 ;;
		*) return 1 ;;
	esac
}

# Normalize true/false (case-insensitive). Prints "true" or "false"; returns 1 if invalid.
parse_bool() {
	case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
		true) printf 'true\n' ;;
		false) printf 'false\n' ;;
		*) return 1 ;;
	esac
}

# CLI flag > INSTALL_CELLAR_GATEWAY env. Empty means unset.
install_gateway_choice=

parse_install_args() {
	while [ "$#" -gt 0 ]; do
		case "$1" in
			--install-cellar-gateway)
				[ "$#" -ge 2 ] || fail "--install-cellar-gateway requires true or false"
				install_gateway_choice=$(parse_bool "$2") ||
					fail "--install-cellar-gateway expects true or false (got: $2)"
				shift 2
				;;
			--install-cellar-gateway=*)
				install_gateway_choice=$(parse_bool "${1#*=}") ||
					fail "--install-cellar-gateway expects true or false (got: ${1#*=})"
				shift
				;;
			INSTALL_CELLAR_GATEWAY | INSTALL_CELLAR_GATEWAY=*)
				fail "set the env var before the command, e.g. INSTALL_CELLAR_GATEWAY=true $0 … (or pass --install-cellar-gateway true)"
				;;
			-h | --help)
				printf 'Usage: %s [--install-cellar-gateway true|false]\n' "$0"
				printf '  Always installs cellar + cellard. Gateway is optional.\n'
				printf '  Env: INSTALL_CELLAR_GATEWAY=true|false ./install.sh\n'
				printf '  Curl: curl … | bash -s -- --install-cellar-gateway true\n'
				printf '  Curl: curl … | INSTALL_CELLAR_GATEWAY=true bash\n'
				exit 0
				;;
			*)
				fail "unknown argument: $1 (try --install-cellar-gateway true|false)"
				;;
		esac
	done
}

parse_install_args "$@"

if [ -z "$install_gateway_choice" ] && [ -n "${INSTALL_CELLAR_GATEWAY:-}" ]; then
	install_gateway_choice=$(parse_bool "$INSTALL_CELLAR_GATEWAY") ||
		fail "INSTALL_CELLAR_GATEWAY expects true or false (got: $INSTALL_CELLAR_GATEWAY)"
fi

prompt_install_gateway() {
	local reply normalized
	while true; do
		printf 'Install cellar-gateway? [y/N] ' >/dev/tty
		IFS= read -r reply </dev/tty || reply=
		normalized=$(printf '%s' "$reply" | tr '[:upper:]' '[:lower:]')
		case "$normalized" in
			y | yes | true) return 0 ;;
			n | no | false | '') return 1 ;;
			*)
				printf 'Please answer y or n (or true/false).\n' >/dev/tty
				;;
		esac
	done
}

resolve_components() {
	local raw item want_gateway
	local -a list=()

	if [ -n "${CELLAR_COMPONENTS:-}" ]; then
		# Allow comma and/or whitespace separators. Full override; skips flag/prompt.
		raw=${CELLAR_COMPONENTS//,/ }
		for item in $raw; do
			[ -n "$item" ] || continue
			is_valid_component "$item" || fail "unknown component: $item (expected: $ALL_COMPONENTS)"
			list+=("$item")
		done
		[ ${#list[@]} -gt 0 ] || fail "CELLAR_COMPONENTS is empty"
		printf 'Using CELLAR_COMPONENTS: %s\n' "${list[*]}"
		selected_components="${list[*]}"
		return 0
	fi

	# shellcheck disable=SC2206
	list=($DEFAULT_COMPONENTS)

	if [ -n "$install_gateway_choice" ]; then
		want_gateway=$install_gateway_choice
	elif [ ! -t 1 ]; then
		# Common misuse: ./install.sh | INSTALL_CELLAR_GATEWAY=true (dead pipe + would hang on prompt).
		_print_misuse() {
			printf 'Note: stdout is not a terminal.\n'
			printf '  Wrong:   %s | INSTALL_CELLAR_GATEWAY=true\n' "$0"
			printf '  Correct: INSTALL_CELLAR_GATEWAY=true %s\n' "$0"
			printf '  Or:      %s --install-cellar-gateway true\n' "$0"
		}
		if ( : >/dev/tty ) 2>/dev/null; then
			_print_misuse >/dev/tty
		else
			_print_misuse >&2
		fi
		unset -f _print_misuse
		exit 1
	elif [ -c /dev/tty ] && [ -r /dev/tty ] && [ -w /dev/tty ]; then
		if prompt_install_gateway; then
			want_gateway=true
		else
			want_gateway=false
		fi
	else
		want_gateway=false
		printf 'No TTY; installing defaults (%s). Use --install-cellar-gateway or INSTALL_CELLAR_GATEWAY to include the gateway.\n' \
			"${list[*]}"
	fi

	if [ "$want_gateway" = true ]; then
		list+=(cellar-gateway)
		printf 'Including cellar-gateway.\n'
	else
		printf 'Skipping cellar-gateway.\n'
	fi

	selected_components="${list[*]}"
}

resolve_components

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
	"$selected_components"

for file in $selected_components; do
	[ -f "$host_dir/$file" ] || fail "$file is missing from the $os release archive"
done

if [ "$os" = "linux" ]; then
	if component_selected cellard; then
		[ -f "$host_dir/contrib/systemd/cellard.service" ] ||
			fail "cellard.service is missing from the release archive"
	fi
	if component_selected cellar-gateway; then
		[ -f "$host_dir/contrib/systemd/cellar-gateway.service" ] ||
			fail "cellar-gateway.service is missing from the release archive"
	fi
	if component_selected cellard || component_selected cellar-gateway; then
		[ -f "$host_dir/contrib/systemd/cellar.sysusers" ] ||
			fail "cellar.sysusers is missing from the release archive"
	fi
elif [ "$os" = "darwin" ]; then
	if component_selected cellard; then
		[ -f "$host_dir/contrib/launchd/com.prodioslabs.cellard.plist" ] ||
			fail "com.prodioslabs.cellard.plist is missing from the release archive"
	fi
	if component_selected cellar-gateway; then
		[ -f "$host_dir/contrib/launchd/com.prodioslabs.cellar-gateway.plist" ] ||
			fail "com.prodioslabs.cellar-gateway.plist is missing from the release archive"
	fi
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
for file in $selected_components; do
	$sudo_cmd install -m 755 "$host_dir/$file" "$PREFIX/bin/$file"
done

install_launchd_plist() {
	local src=$1
	local dest=$2
	sed -e "s|@BINDIR@|$runtime_bindir|g" \
		-e "s|@DATA_DIR@|$CELLAR_DATA_DIR|g" \
		-e "s|@LOG_DIR@|$cellar_log_dir|g" \
		-e "s|@HOME@|$install_home|g" \
		"$src" >"$dest"
	chmod 644 "$dest"
}

if [ "$os" = "darwin" ]; then
	if component_selected cellard || component_selected cellar-gateway; then
		install -d "$CELLAR_DATA_DIR"

		launchd_agent_dir="${CELLAR_LAUNCHD_AGENT_DIR:-$install_home/Library/LaunchAgents}"
		cellar_log_dir="${CELLAR_LOG_DIR:-$install_home/Library/Logs/cellar}"
		install -d "$launchd_agent_dir" "$cellar_log_dir"
		runtime_bindir="$PREFIX/bin"

		if component_selected cellard; then
			install_launchd_plist \
				"$host_dir/contrib/launchd/com.prodioslabs.cellard.plist" \
				"$launchd_agent_dir/com.prodioslabs.cellard.plist"
		fi
		if component_selected cellar-gateway; then
			install_launchd_plist \
				"$host_dir/contrib/launchd/com.prodioslabs.cellar-gateway.plist" \
				"$launchd_agent_dir/com.prodioslabs.cellar-gateway.plist"
		fi
		printf 'Installed LaunchAgents (not loaded) under %s\n' "$launchd_agent_dir"
	fi
fi

if [ "$os" = "linux" ]; then
	need_units=0
	component_selected cellard && need_units=1
	component_selected cellar-gateway && need_units=1

	if [ "$need_units" -eq 1 ]; then
		$sudo_cmd install -d "$SYSTEMD_UNIT_DIR"
		if component_selected cellard; then
			$sudo_cmd install -m 644 \
				"$host_dir/contrib/systemd/cellard.service" \
				"$SYSTEMD_UNIT_DIR/cellard.service"
		fi
		if component_selected cellar-gateway; then
			$sudo_cmd install -m 644 \
				"$host_dir/contrib/systemd/cellar-gateway.service" \
				"$SYSTEMD_UNIT_DIR/cellar-gateway.service"
		fi

		$sudo_cmd install -d "$SYSUSERS_DIR"
		$sudo_cmd install -m 644 \
			"$host_dir/contrib/systemd/cellar.sysusers" \
			"$SYSUSERS_DIR/cellar.conf"
	fi
fi

printf '\nCellar %s installed successfully (%s).\n' "$VERSION" "$selected_components"

if [ "$os" = "linux" ]; then
	if component_selected cellard; then
		printf '\nHosts need KVM (/dev/kvm). cellard will EnsureInstalled microsandbox on first use.\n'
	fi
	if component_selected cellard || component_selected cellar-gateway; then
		printf '\nRun these commands to create the service user and start Cellar:\n'
		printf '  sudo systemd-sysusers\n'
		printf '  sudo systemctl daemon-reload\n'
		if component_selected cellard; then
			printf '  sudo systemctl enable --now cellard\n'
		fi
		if component_selected cellar-gateway; then
			printf '  sudo systemctl enable --now cellar-gateway\n'
		fi
	elif component_selected cellar; then
		printf '\nCLI installed to %s/bin/cellar.\n' "$PREFIX"
	fi
else
	printf '\nNext steps on macOS:\n'
	step=1
	if component_selected cellard; then
		printf '  %d. Ensure Virtualization.framework / KVM-equivalent access is available\n' "$step"
		step=$((step + 1))
		printf '  %d. Load the daemon:   launchctl bootstrap gui/$(id -u) %s/com.prodioslabs.cellard.plist\n' \
			"$step" "${CELLAR_LAUNCHD_AGENT_DIR:-$install_home/Library/LaunchAgents}"
		step=$((step + 1))
		printf '  %d. Initialize:        cellar init --advertise-addr 127.0.0.1:17946\n' "$step"
		step=$((step + 1))
	fi
	if component_selected cellar-gateway; then
		printf '  %d. Load the gateway:  launchctl bootstrap gui/$(id -u) %s/com.prodioslabs.cellar-gateway.plist\n' \
			"$step" "${CELLAR_LAUNCHD_AGENT_DIR:-$install_home/Library/LaunchAgents}"
		step=$((step + 1))
	fi
	if component_selected cellard || component_selected cellar-gateway; then
		printf '  Data and socket live under %s.\n' "$CELLAR_DATA_DIR"
		printf '  Logs: %s\n' "${CELLAR_LOG_DIR:-$install_home/Library/Logs/cellar}"
	fi
	case ":$PATH:" in
		*"$PREFIX/bin"*) ;;
		*)
			printf '  Warning: %s/bin is not on PATH; add it so installed binaries are found.\n' "$PREFIX"
			;;
	esac
fi
