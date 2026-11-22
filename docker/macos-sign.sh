#!/bin/sh
# Signs and notarizes one macOS binary after goreleaser builds it, from Linux,
# with rcodesign. Every build's post hook runs it; a target that is not darwin
# returns at once.
#
#   docker/macos-sign.sh <binary> <os>
#
# Credentials live in .signing/, which git and Docker ignore:
#
#   developer-id.p12            Developer ID Application certificate and key
#   developer-id.p12.password   the password the .p12 was exported with
#   notary-api-key.json         App Store Connect API key, from
#                               rcodesign encode-app-store-connect-api-key
#
# With the certificate the binary is signed with the hardened runtime, which
# notarization requires. With the API key as well it is zipped, submitted to
# Apple's notary service, and waited on; a bare Mach-O cannot carry a stapled
# ticket, so Gatekeeper looks the ticket up online the first time it runs.
# MACOS_NOTARIZE=0 signs without notarizing, for a snapshot that should not
# wait on Apple. Without the certificate the binary is left as the Go linker
# wrote it, so a build without credentials still works.
set -eu

binary=$1
os=$2
[ "$os" = darwin ] || exit 0

dir=${KARAKURI_SIGNING_DIR:-.signing}
p12=$dir/developer-id.p12
p12_password=$dir/developer-id.p12.password
api_key=$dir/notary-api-key.json

if [ ! -f "$p12" ]; then
    exit 0
fi
if [ ! -f "$p12_password" ]; then
    echo "macos-sign: $p12 needs its password in $p12_password" >&2
    exit 1
fi

# The identifier is what the designated requirement names, so it must not
# change between releases: the agent's macOS Accessibility grant is keyed to it.
name=$(basename "$binary")
entitlements=
case "$binary" in
*/plugins/*) identifier=org.karakuri.plugin.$name ;;
*)
    case "$name" in
    karakuri)
        identifier=org.karakuri.server
        entitlements=docker/macos-server.entitlements
        ;;
    karakuri-agent) identifier=karakuri-agent ;;
    karakuri-auto) identifier=org.karakuri.auto ;;
    *) identifier=org.karakuri.$name ;;
    esac
    ;;
esac

# rcodesign narrates every step on stderr, which across every darwin build
# buries the release log, so its output is shown only when it fails.
if ! out=$(rcodesign sign --p12-file "$p12" --p12-password-file "$p12_password" \
    --for-notarization --binary-identifier "$identifier" \
    ${entitlements:+--entitlements-xml-file "$entitlements"} "$binary" 2>&1); then
    echo "$out" >&2
    exit 1
fi
echo "macos-sign: signed $binary as $identifier"

if [ ! -f "$api_key" ] || [ "${MACOS_NOTARIZE:-1}" = 0 ]; then
    exit 0
fi

# An hour rather than rcodesign's default ten minutes: Apple holds a team's
# first submissions for longer, and a release that stops waiting ships
# binaries Gatekeeper refuses.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
zip -qj "$work/$name.zip" "$binary"
rcodesign notary-submit --api-key-file "$api_key" --max-wait-seconds 3600 --wait \
    "$work/$name.zip" >&2
echo "macos-sign: notarized $binary"
