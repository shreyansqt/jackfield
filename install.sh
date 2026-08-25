#!/bin/sh
#
# Install the jf CLI on this machine.
#
#   curl -fsSL https://raw.githubusercontent.com/shreyansqt/jackfield/main/install.sh | sh
#
# The script downloads the latest release binary for this machine, verifies its
# checksum, and installs it in ~/.local/bin. It needs no Go toolchain, no clone,
# and no sudo. Run it again to upgrade in place.
#
# Environment overrides:
#   JF_INSTALL_DIR  where to put the jf binary (default ~/.local/bin)
#   JF_MAN_DIR      where to put the man page (default ~/.local/share/man/man1)
#   JF_VERSION      a release tag such as v1.2.3 (default: the latest release)
#   JF_REPO         owner/name of the source repository

set -eu

repo=${JF_REPO:-shreyansqt/jackfield}
install_dir=${JF_INSTALL_DIR:-${HOME}/.local/bin}
man_dir=${JF_MAN_DIR:-${HOME}/.local/share/man/man1}
requested_version=${JF_VERSION:-latest}
tmp_dir=
man_installed=0

# --- output -----------------------------------------------------------------

# Colour only when standard error is a terminal. Under `curl | sh` the standard
# input is a pipe, so the terminal test must use standard error.
if [ -t 2 ]; then
	bold=$(printf '\033[1m')
	dim=$(printf '\033[2m')
	red=$(printf '\033[31m')
	green=$(printf '\033[32m')
	reset=$(printf '\033[0m')
else
	bold=''
	dim=''
	red=''
	green=''
	reset=''
fi

say() {
	printf '%s\n' "$*" >&2
}

step() {
	printf '%s==>%s %s\n' "$dim" "$reset" "$*" >&2
}

fail() {
	printf '%serror:%s %s\n' "$red" "$reset" "$*" >&2
	exit 1
}

cleanup() {
	if [ -n "$tmp_dir" ] && [ -d "$tmp_dir" ]; then
		rm -rf "$tmp_dir"
	fi
}
trap cleanup EXIT INT TERM

# --- platform ---------------------------------------------------------------

# detect_os maps `uname -s` to the name goreleaser puts in the asset.
detect_os() {
	os=$(uname -s)
	case "$os" in
	Darwin) printf 'darwin' ;;
	Linux) printf 'linux' ;;
	*) fail "unsupported operating system: $os. jf ships for macOS and Linux." ;;
	esac
}

# detect_arch maps `uname -m` to the name goreleaser puts in the asset.
detect_arch() {
	arch=$(uname -m)
	case "$arch" in
	x86_64 | amd64) printf 'amd64' ;;
	arm64 | aarch64) printf 'arm64' ;;
	*) fail "unsupported processor: $arch. jf ships for amd64 and arm64." ;;
	esac
}

# --- download ---------------------------------------------------------------

# have reports whether a command exists.
have() {
	command -v "$1" >/dev/null 2>&1
}

# download fetches a URL to a file. It prefers curl and accepts wget, because a
# bare Linux image often has only one of them.
download() {
	url=$1
	destination=$2
	if have curl; then
		# --location follows the GitHub redirect to the asset storage host.
		curl -fsSL --retry 3 --retry-delay 1 -o "$destination" "$url" || return 1
	elif have wget; then
		wget -qO "$destination" "$url" || return 1
	else
		fail "this script needs curl or wget, and found neither."
	fi
}

# checksum prints the sha256 of a file. macOS has shasum, most Linux images have
# sha256sum, and a few have only openssl.
checksum() {
	file=$1
	if have sha256sum; then
		sha256sum "$file" | awk '{print $1}'
	elif have shasum; then
		shasum -a 256 "$file" | awk '{print $1}'
	elif have openssl; then
		openssl dgst -sha256 "$file" | awk '{print $NF}'
	else
		printf ''
	fi
}

# --- install ----------------------------------------------------------------

main() {
	os=$(detect_os)
	arch=$(detect_arch)
	asset="jf_${os}_${arch}.tar.gz"

	# The /releases/latest/download/ form redirects to the newest release, so
	# the script reads no JSON and needs no jq.
	if [ "$requested_version" = latest ]; then
		base_url="https://github.com/${repo}/releases/latest/download"
		version_label="the latest release"
	else
		base_url="https://github.com/${repo}/releases/download/${requested_version}"
		version_label="$requested_version"
	fi

	step "Installing jf for ${os}/${arch} from ${version_label}."

	tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t jackfield)
	[ -n "$tmp_dir" ] && [ -d "$tmp_dir" ] || fail "could not create a temporary directory."

	step "Downloading ${asset}."
	if ! download "${base_url}/${asset}" "${tmp_dir}/${asset}"; then
		fail "could not download ${base_url}/${asset}
  Check the machine's network, and check that a release exists at
  https://github.com/${repo}/releases"
	fi

	# Verify the checksum. A download that a proxy truncated, or an error page
	# that arrived with a 200 status, both fail here rather than at first run.
	step "Verifying the checksum."
	if ! download "${base_url}/checksums.txt" "${tmp_dir}/checksums.txt"; then
		fail "could not download the checksums file. Refusing to install an unverified binary."
	fi

	expected=$(awk -v want="$asset" '$2 == want || $2 == "*" want {print $1; exit}' "${tmp_dir}/checksums.txt")
	[ -n "$expected" ] || fail "the checksums file has no entry for ${asset}."

	actual=$(checksum "${tmp_dir}/${asset}")
	if [ -z "$actual" ]; then
		fail "this machine has no sha256 tool (sha256sum, shasum, or openssl).
  Refusing to install an unverified binary."
	fi
	if [ "$actual" != "$expected" ]; then
		fail "the checksum does not match for ${asset}.
  expected: ${expected}
  actual:   ${actual}
  Refusing to install. Try again, and report it if it repeats."
	fi

	step "Unpacking."
	tar -xzf "${tmp_dir}/${asset}" -C "$tmp_dir" jf 2>/dev/null ||
		tar -xzf "${tmp_dir}/${asset}" -C "$tmp_dir" ||
		fail "could not unpack ${asset}."
	[ -f "${tmp_dir}/jf" ] || fail "the archive holds no jf binary."
	chmod +x "${tmp_dir}/jf"

	mkdir -p "$install_dir" || fail "could not create ${install_dir}."

	# Move the new binary over the old one through a temporary name in the same
	# directory. A running jf keeps its open file, and the replacement is atomic,
	# so an upgrade never leaves a half-written binary on PATH.
	target="${install_dir}/jf"
	staged="${install_dir}/.jf.new.$$"
	if ! mv -f "${tmp_dir}/jf" "$staged" 2>/dev/null; then
		cp "${tmp_dir}/jf" "$staged" || fail "could not write to ${install_dir}."
	fi
	chmod 755 "$staged"
	mv -f "$staged" "$target" || {
		rm -f "$staged"
		fail "could not install to ${target}."
	}

	installed_version=$("$target" --version 2>/dev/null || printf 'jf (version unknown)')
	printf '%s\n' "${green}Installed ${installed_version} in ${target}${reset}" >&2

	install_man_page

	report_next_steps "$target"
}

# install_man_page puts jf.1 under the home directory, so `man jf` works without
# sudo. The page is optional: a machine that cannot fetch it still has a working
# jf, and `jf --help` says the same things. So a failure here is a note, not an
# error.
install_man_page() {
	man_installed=0

	# The release archive may carry the page. Prefer that copy, because it
	# matches the binary that was just installed.
	if [ -f "${tmp_dir}/jf.1" ]; then
		source_page="${tmp_dir}/jf.1"
	elif [ -f "${tmp_dir}/docs/man/jf.1" ]; then
		source_page="${tmp_dir}/docs/man/jf.1"
	else
		# Otherwise fetch it from the repository at the installed version.
		if [ "$requested_version" = latest ]; then
			man_ref=main
		else
			man_ref=$requested_version
		fi
		source_page="${tmp_dir}/jf.1.fetched"
		if ! download "https://raw.githubusercontent.com/${repo}/${man_ref}/docs/man/jf.1" "$source_page"; then
			return 0
		fi
	fi

	mkdir -p "$man_dir" 2>/dev/null || return 0
	cp "$source_page" "${man_dir}/jf.1" 2>/dev/null || return 0
	chmod 644 "${man_dir}/jf.1" 2>/dev/null || true
	man_installed=1
	step "Installed the man page in ${man_dir}/jf.1."
}

# on_path reports whether the install directory is already on PATH.
on_path() {
	case ":${PATH}:" in
	*":${install_dir}:"*) return 0 ;;
	*) return 1 ;;
	esac
}

# shell_profile guesses the file that adds a directory to PATH for this shell.
shell_profile() {
	case "${SHELL:-}" in
	*/zsh) printf '%s/.zshrc' "$HOME" ;;
	*/bash)
		if [ -f "${HOME}/.bash_profile" ]; then
			printf '%s/.bash_profile' "$HOME"
		else
			printf '%s/.bashrc' "$HOME"
		fi
		;;
	*/fish) printf '%s/.config/fish/config.fish' "$HOME" ;;
	*) printf '%s/.profile' "$HOME" ;;
	esac
}

report_next_steps() {
	target=$1
	say ''

	# The PATH hint prints only when it is needed.
	if ! on_path; then
		profile=$(shell_profile)
		say "${bold}Add ${install_dir} to your PATH${reset}"
		case "$profile" in
		*config.fish)
			say "  echo 'fish_add_path ${install_dir}' >> ${profile}"
			;;
		*)
			say "  echo 'export PATH=\"${install_dir}:\$PATH\"' >> ${profile}"
			;;
		esac
		say "  Then open a new shell, or run: export PATH=\"${install_dir}:\$PATH\""
		say ''
	fi

	# The MANPATH hint prints only when `man jf` does not already work. Most
	# machines search ~/.local/share/man on their own, and those say nothing.
	if [ "$man_installed" = 1 ] && ! man -w jf >/dev/null 2>&1; then
		man_root=$(dirname -- "$man_dir")
		profile=$(shell_profile)
		say "${bold}Add ${man_root} to your MANPATH${reset} so that 'man jf' works"
		case "$profile" in
		*config.fish)
			say "  set -gx MANPATH ${man_root} \$MANPATH  # in ${profile}"
			;;
		*)
			say "  echo 'export MANPATH=\"${man_root}:\$MANPATH\"' >> ${profile}"
			;;
		esac
		say ''
	fi

	say "${bold}Next, two steps${reset}"
	say ''
	say "  ${bold}1. Point jf at your hub.${reset}"
	say "     Set it for one shell:"
	say "       export JF_HUB=https://your-hub.workers.dev"
	say "     Or set it for every shell, in ~/.config/jackfield/jackfield.yaml:"
	say "       hub: https://your-hub.workers.dev"
	say ''
	say "  ${bold}2. Sign this machine in.${reset}"
	say "       jf login"
	say ''
	say "     jf login opens a browser. On a headless machine or over SSH it"
	say "     prints a code to type on another device instead."
	say ''
	say "Then run ${bold}jf status${reset} to see where every connection stands."
	say ''
	say "Run ${bold}jf --help${reset} to see every command, or ${bold}man jf${reset} to read the manual."
	say "An AI agent reads ${bold}jf schema --json${reset} for the whole command tree as JSON."
	say "Docs: https://github.com/${repo}/blob/main/docs/cli.md"
}

main "$@"
