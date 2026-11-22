#!/bin/sh
# Container entrypoint. Generates /etc/karakuri/config.yaml on first start and
# then execs karakuri with whatever the command line asked for.
#
# The secrets are minted here rather than baked into the image for the reason
# install.sh mints them at install time: `config gen` emits the shared
# placeholders, and every container started from an image carrying those would
# share a JWT signing key and an encryption key with every other one. First
# start is the moment they can change, because nothing is encrypted yet.
set -eu

CONFIG=/etc/karakuri/config.yaml

random_hex() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
    else
        od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
    fi
}

if [ ! -f "$CONFIG" ]; then
    echo "karakuri: generating $CONFIG with fresh secrets"
    tmp=$(mktemp)
    karakuri config gen --env=prod 2>/dev/null > "$tmp"
    jwt_secret=$(random_hex)
    encryption_key=$(random_hex)
    sed -e "s|^\(  jwt_secret: \).*|\1\"$jwt_secret\"|" \
        -e "s|^\(  encryption_key: \).*|\1\"$encryption_key\"|" \
        "$tmp" > "$CONFIG"
    rm -f "$tmp"
    if ! grep -q "jwt_secret: \"$jwt_secret\"" "$CONFIG"; then
        rm -f "$CONFIG"
        echo "karakuri: could not write generated secrets into $CONFIG" >&2
        exit 1
    fi
fi

exec karakuri "$@"
