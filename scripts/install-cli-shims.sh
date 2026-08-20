#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(dirname -- "$script_dir")
user_home=$HOME
install_dir=${JF_INSTALL_DIR:-$user_home/.local/lib/jackfield}
config_dir=${JF_CONFIG_DIR:-$user_home/.config/jackfield}
shim_dir=${JF_SHIM_DIR:-$user_home/.local/jackfield/bin}

mkdir -p "$install_dir" "$config_dir" "$shim_dir"
(cd "$repo_dir" && GOCACHE=${GOCACHE:-/private/tmp/jackfield-go-cache} go build -o "$install_dir/jf" ./cmd/jf)
ln -sfn "$repo_dir/jackfield.yaml" "$config_dir/jackfield.yaml"

for name in jf gog wrangler aws; do
	target="$shim_dir/$name"
	if [ -e "$target" ] && [ ! -L "$target" ]; then
		echo "Jackfield will not replace the existing file: $target" >&2
		exit 1
	fi
	destination="$install_dir/jf"
	if [ -L "$target" ] && [ "$(readlink "$target")" != "$destination" ]; then
		echo "Jackfield will not replace the existing link: $target" >&2
		exit 1
	fi
done

for name in jf gog wrangler aws; do
	ln -sfn "$install_dir/jf" "$shim_dir/$name"
done

echo "Installed Jackfield CLI shims in $shim_dir"
