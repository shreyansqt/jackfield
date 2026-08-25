#!/bin/sh
#
# Install the gog/wrangler/aws command shims for a machine that gates CLIs.
#
# This is the optional second step. A plain machine only needs the jf binary,
# which install.sh at the repository root installs. Run this script on a machine
# where `gog`, `wrangler`, and `aws` must go through the workspace gate.
#
# The script finds jf in this order:
#   1. $JF_BINARY, if it is set.
#   2. A Go toolchain plus this repository: it builds jf from source.
#   3. An installed jf on PATH, such as the one install.sh puts in ~/.local/bin.
#
# Source order note: this repository builds first when a Go toolchain is here,
# because a developer editing jf expects the shims to run the code just edited.
# Use JF_BINARY to override that, or JF_FROM_PATH=1 to skip the build.

set -eu

# shellcheck disable=SC1007  # `CDPATH= cd` clears CDPATH for this one command.
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(dirname -- "$script_dir")
user_home=$HOME
install_dir=${JF_INSTALL_DIR:-$user_home/.local/lib/jackfield}
config_dir=${JF_CONFIG_DIR:-$user_home/.config/jackfield}
shim_dir=${JF_SHIM_DIR:-$user_home/.local/jackfield/bin}

have() {
	command -v "$1" >/dev/null 2>&1
}

mkdir -p "$config_dir" "$shim_dir"

# Choose the jf binary that the shims will point at.
if [ -n "${JF_BINARY:-}" ]; then
	[ -x "$JF_BINARY" ] || {
		echo "JF_BINARY is not an executable file: $JF_BINARY" >&2
		exit 1
	}
	jf_binary=$JF_BINARY
	source_note="the binary in JF_BINARY"
elif [ "${JF_FROM_PATH:-0}" != 1 ] && have go && [ -f "$repo_dir/go.mod" ]; then
	mkdir -p "$install_dir"
	(cd "$repo_dir" && GOCACHE=${GOCACHE:-/private/tmp/jackfield-go-cache} go build -o "$install_dir/jf" ./cmd/jf)
	jf_binary=$install_dir/jf
	source_note="a fresh build of this repository"
elif have jf; then
	jf_binary=$(command -v jf)
	source_note="the installed jf on PATH"
else
	echo "Jackfield found no jf binary to link." >&2
	echo "Install jf first:" >&2
	echo "  curl -fsSL https://raw.githubusercontent.com/shreyansqt/jackfield/main/install.sh | sh" >&2
	echo "Or install a Go toolchain and run this script inside a clone." >&2
	exit 1
fi

# The shims must not point at a link inside the shim directory, because that
# link would then resolve to itself.
case "$jf_binary" in
"$shim_dir"/*)
	echo "Refusing to link the shims at a binary inside $shim_dir: $jf_binary" >&2
	echo "Set JF_BINARY to the real jf binary." >&2
	exit 1
	;;
esac

# Link the manifest only when this clone has one. A machine that installed jf
# without a clone keeps whatever manifest it already wrote.
if [ -f "$repo_dir/jackfield.yaml" ]; then
	ln -sfn "$repo_dir/jackfield.yaml" "$config_dir/jackfield.yaml"
fi

for name in jf gog wrangler aws; do
	target="$shim_dir/$name"
	if [ -e "$target" ] && [ ! -L "$target" ]; then
		echo "Jackfield will not replace the existing file: $target" >&2
		exit 1
	fi
	if [ -L "$target" ] && [ "$(readlink "$target")" != "$jf_binary" ]; then
		echo "Jackfield will not replace the existing link: $target" >&2
		echo "It points at $(readlink "$target") rather than $jf_binary." >&2
		echo "Remove it and run this script again to move to the new binary." >&2
		exit 1
	fi
done

for name in jf gog wrangler aws; do
	ln -sfn "$jf_binary" "$shim_dir/$name"
done

echo "Installed Jackfield CLI shims in $shim_dir, from $source_note."
echo "Put $shim_dir first on PATH so the shims win over the real commands."
