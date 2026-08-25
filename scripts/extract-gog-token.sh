#!/usr/bin/env bash
#
# extract-gog-token.sh — build the gog-personal credential JSON for the hub.
#
# The hub is a vault. It holds one durable secret per connection. For gog that
# secret is the Google refresh token, wrapped in the JSON convention that
# `jf cred install gog-personal` reads back.
#
# This script prints that JSON to stdout. Pipe it into `jf cred set`:
#
#   scripts/extract-gog-token.sh shreyansqt@gmail.com \
#     | jf cred set --stdin --ticket <TICKET> --identity shreyansqt@gmail.com gog-personal
#
# It never prints the token to a terminal on its own, and it deletes every
# temporary file it makes.
#
# Two ways to get the token out of gog:
#
#   1. EXPORT (default). gog v0.31.0 has `gog auth tokens export`, which reads
#      the token this machine already holds in its real keyring. No re-auth.
#
#   2. RE-AUTH (--reauth). For a machine with no stored token: run `gog auth add`
#      into a throwaway GOG_HOME with the file keyring backend, read the token
#      from that dir, then wipe the dir. This is the only flow that opens a
#      browser and contacts Google.
#
# The refresh token expires weekly until the OAuth app is published, so this is
# a recurring step, not a one-time migration.

set -euo pipefail

EMAIL="${1:-}"
MODE="export"
if [ "${2:-}" = "--reauth" ] || [ "${1:-}" = "--reauth" ]; then
	MODE="reauth"
	[ "${1:-}" = "--reauth" ] && EMAIL="${2:-}"
fi

if [ -z "$EMAIL" ]; then
	echo "usage: $0 <email> [--reauth]" >&2
	exit 2
fi

# The real gog binary, not the jackfield shim. The shim denies -a and --home,
# which both flows here need. GOG_BIN overrides it for an unusual install.
GOG_BIN="${GOG_BIN:-/opt/homebrew/bin/gog}"
if [ ! -x "$GOG_BIN" ]; then
	GOG_BIN="$(command -v gog || true)"
fi
if [ -z "$GOG_BIN" ] || [ ! -x "$GOG_BIN" ]; then
	echo "cannot find the real gog binary; set GOG_BIN to its path" >&2
	exit 1
fi

# The OAuth client id is metadata only; the import does not need it, because the
# gog client credentials already live in the gog config. Pass it in through the
# CLIENT_ID environment variable to record it for provenance; otherwise it stays
# empty and the credential is still complete.
CLIENT_ID="${CLIENT_ID:-}"

# A throwaway file that the export writes into. mode 0600, wiped on exit.
WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/gog-extract.XXXXXX")"
chmod 700 "$WORKDIR"
EXPORT_FILE="$WORKDIR/token.json"

cleanup() {
	# Overwrite before delete, so the token does not linger in a freed block.
	if [ -f "$EXPORT_FILE" ]; then
		dd if=/dev/zero of="$EXPORT_FILE" bs=1k count=4 conv=notrunc 2>/dev/null || true
	fi
	rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

if [ "$MODE" = "reauth" ]; then
	# The isolated re-auth flow, for a machine with no stored token.
	export GOG_HOME="$WORKDIR/home"
	mkdir -p "$GOG_HOME"
	: "${GOG_KEYRING_PASSWORD:?set GOG_KEYRING_PASSWORD to a throwaway passphrase for the temp keyring}"
	"$GOG_BIN" config set keyring_backend file >/dev/null
	echo "A browser will open for the Google sign-in. Complete it as $EMAIL." >&2
	"$GOG_BIN" auth add "$EMAIL" >&2
	"$GOG_BIN" auth tokens export "$EMAIL" --out "$EXPORT_FILE" >/dev/null 2>&1
else
	# The default export flow, reading this machine's existing real keyring.
	"$GOG_BIN" auth tokens export "$EMAIL" --out "$EXPORT_FILE" --overwrite >/dev/null 2>&1
fi

if [ ! -s "$EXPORT_FILE" ]; then
	echo "gog wrote no token file; is $EMAIL authorized in gog on this machine?" >&2
	exit 1
fi

# Reshape gog's export file into the hub convention. gog exports:
#   {"email":..., "client":..., "created_at":..., "refresh_token":...}
# The hub stores refresh_token + email + client + client_id.
REFRESH_TOKEN="$(sed -n 's/.*"refresh_token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$EXPORT_FILE")"
CLIENT="$(sed -n 's/.*"client"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$EXPORT_FILE")"
[ -z "$CLIENT" ] && CLIENT="default"

if [ -z "$REFRESH_TOKEN" ]; then
	echo "could not read the refresh token from gog's export" >&2
	exit 1
fi

# Print the hub JSON. This is the only line on stdout, so it pipes cleanly into
# `jf cred set --stdin`.
printf '{"refresh_token":"%s","email":"%s","client":"%s","client_id":"%s"}\n' \
	"$REFRESH_TOKEN" "$EMAIL" "$CLIENT" "$CLIENT_ID"
