#!/bin/sh
# Installs karakuri's files from an unpacked release archive, or from build/
# after `make`. Who runs it decides what kind of install it is, the same way
# `karakuri service install` decides which service manager it writes to:
#
#   root                 a system install: binaries in /usr/local/bin, resources
#                        in /usr/share/karakuri, config in /etc/karakuri, state
#                        in /var/lib/karakuri, and the account a system service
#                        runs as.
#   anyone else          a user install under their home directory: ~/.local/bin,
#                        ~/.local/share/karakuri, and ~/.config/karakuri. Nothing
#                        outside $HOME is written and no password is asked for.
#   root with --user     the same user install, performed on that account's
#                        behalf.
#
# It does not register a service: `karakuri service install` and
# `karakuri service start` do that afterwards, and the closing message says
# how. Re-running it is an upgrade. The configuration, the database, and any
# registered service are left alone, and everything else is replaced.
set -eu

SRC=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TARGET_USER=
SERVICE_USER=karakuri
PREFIX=

# Where a system install puts what the server did not bring with it. These are
# the paths internal/resources searches, so they are not configurable.
RESOURCE_DIR=/usr/share/karakuri
CONFIG_DIR=/etc/karakuri
DATA_DIR=/var/lib/karakuri

usage() {
    cat <<EOF
Usage: $0 [options]

  --src DIR            Directory holding the karakuri binary and its resources
                       (default: the directory this script is in)
  --user NAME          Install for this account rather than system-wide. Only
                       root may name an account other than its own.
  --service-user NAME  Account a system service runs as, created when missing
                       and given the data directory (default: karakuri)
  --prefix DIR         Put binaries in DIR/bin (default: /usr/local system-wide,
                       ~/.local for a user install)

Everything else follows the paths the binary searches, so it is not
configurable. To keep an install somewhere else entirely, unpack the archive
there and run ./karakuri: a directory holding the binary and its resources is
found without being installed at all.
  -h, --help           Print this message
EOF
}

die() {
    printf 'Error: %s\n' "$1" >&2
    exit 1
}

warn() {
    printf 'Warning: %s\n' "$1" >&2
}

# random_hex prints 32 random bytes as hex, the shape both secrets take.
random_hex() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
    else
        od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
    fi
}

# copy_resources replaces each resource directory rather than copying over it,
# so a widget or theme dropped since the last install does not linger. runtimes
# is optional: the CPython module is a separate download. The web interface is
# compiled into the binary, so the frontend/ an earlier version installed is
# removed rather than left to look current.
copy_resources() {
    dest=$1
    rm -rf "${dest:?}/frontend"
    for dir in plugins themes runtimes; do
        [ -d "$SRC/$dir" ] || continue
        rm -rf "${dest:?}/$dir"
        mkdir -p "$dest/$dir"
        cp -R "$SRC/$dir/." "$dest/$dir/"
    done
    if [ ! -f "$dest/runtimes/python.wasm" ]; then
        printf '    no python runtime installed; Python scripts will be unavailable\n'
    fi
}

# generate_config writes a configuration with freshly minted secrets, and does
# nothing when one is already there. `config gen` emits the shared placeholder
# secrets, so they are replaced here: a fresh install is the one moment
# security.encryption_key can change without `karakuri config rotate-key`
# having to migrate stored secrets to it.
generate_config() {
    binary=$1
    config=$2
    if [ -f "$config" ]; then
        printf '    configuration exists, keeping it\n'
        return
    fi
    printf '    generating configuration with fresh secrets...\n'
    tmp=$(mktemp)
    "$binary" config gen --env=prod 2>/dev/null > "$tmp"
    jwt_secret=$(random_hex)
    encryption_key=$(random_hex)
    sed -e "s|^\(  jwt_secret: \).*|\1\"$jwt_secret\"|" \
        -e "s|^\(  encryption_key: \).*|\1\"$encryption_key\"|" \
        "$tmp" > "$config"
    rm -f "$tmp"
    # The file carries the secrets that were just generated, and the umask a
    # home directory install runs under would leave it world-readable.
    chmod 600 "$config"
    if ! grep -q "jwt_secret: \"$jwt_secret\"" "$config" ||
       ! grep -q "encryption_key: \"$encryption_key\"" "$config"; then
        rm -f "$config"
        die "could not write generated secrets into $config"
    fi
}

while [ $# -gt 0 ]; do
    case "$1" in
        --src) [ $# -ge 2 ] || die "--src needs a directory"; SRC=$2; shift 2 ;;
        --user) [ $# -ge 2 ] || die "--user needs a name"; TARGET_USER=$2; shift 2 ;;
        --service-user) [ $# -ge 2 ] || die "--service-user needs a name"; SERVICE_USER=$2; shift 2 ;;
        --prefix) [ $# -ge 2 ] || die "--prefix needs a directory"; PREFIX=$2; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) usage >&2; die "unknown option '$1'" ;;
    esac
done

# Verify what is being installed. The plugins check is what tells an unpacked
# release from some other directory that happens to hold a binary.
[ "$(uname -s)" = "Linux" ] || [ "$(uname -s)" = "Darwin" ] ||
    die "this installer targets Linux and macOS. The archive is self-contained:
run ./karakuri serve from where you unpacked it, and \`karakuri service install\`
registers it with the local service manager."
[ -x "$SRC/karakuri" ] || die "no karakuri binary in $SRC (use --src)"
[ -d "$SRC/plugins" ] || die "no plugins/ in $SRC; this is not an unpacked release"

if [ "$(id -u)" -eq 0 ] && [ -z "$TARGET_USER" ]; then
    # ── System install ────────────────────────

    [ "$(uname -s)" = "Linux" ] || die "a system install writes /usr/share/karakuri, which macOS keeps on
the read-only system volume. Install for an account instead: $0 --user $(id -un)"

    [ -n "$PREFIX" ] || PREFIX=/usr/local
    printf '==> Installing karakuri system-wide from %s\n' "$SRC"
    mkdir -p "$PREFIX/bin" "$RESOURCE_DIR" "$CONFIG_DIR" "$DATA_DIR"

    # The portable commands ride along when present, which is the case for a
    # build/ directory and not for a server archive. Each also has an archive
    # of its own.
    printf '    installing binaries...\n'
    install -m 755 "$SRC/karakuri" "$PREFIX/bin/karakuri"
    if [ -x "$SRC/karakuri-agent" ]; then
        install -m 755 "$SRC/karakuri-agent" "$PREFIX/bin/karakuri-agent"
    fi
    if [ -x "$SRC/karakuri-auto" ]; then
        install -m 755 "$SRC/karakuri-auto" "$PREFIX/bin/karakuri-auto"
    fi

    printf '    installing resources...\n'
    copy_resources "$RESOURCE_DIR"
    generate_config "$PREFIX/bin/karakuri" "$CONFIG_DIR/config.yaml"

    # The service account owns the data directory and nothing else, so a
    # compromised server reaches its own state and not the resources it loads.
    if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
        printf '    creating %s user...\n' "$SERVICE_USER"
        nologin_shell=$(command -v nologin 2>/dev/null || true)
        [ -n "$nologin_shell" ] || nologin_shell=/sbin/nologin
        if command -v useradd >/dev/null 2>&1; then
            useradd --system --shell "$nologin_shell" --home-dir "$DATA_DIR" "$SERVICE_USER"
        elif command -v adduser >/dev/null 2>&1; then
            adduser -S -D -s "$nologin_shell" -h "$DATA_DIR" "$SERVICE_USER"
        else
            die "no useradd or adduser; create the $SERVICE_USER account and re-run"
        fi
    fi

    chown -R root:root "$RESOURCE_DIR"
    chmod -R a+rX "$RESOURCE_DIR"
    chown root:"$SERVICE_USER" "$CONFIG_DIR/config.yaml"
    chmod 640 "$CONFIG_DIR/config.yaml"
    chown -R "$SERVICE_USER":"$SERVICE_USER" "$DATA_DIR"

    # Only the account a service runs as owns the database, so the first login
    # is created as that account, or the database file would belong to root.
    service_flag=
    if [ "$SERVICE_USER" != karakuri ]; then
        service_flag=" --user $SERVICE_USER"
    fi

    cat <<EOF
==> Installation complete

    Register the service:   sudo karakuri service install$service_flag
    Start it:               sudo karakuri service start
    Create the first login: sudo -u $SERVICE_USER karakuri user create admin admin@example.com <password> --admin

    After an upgrade, restart the service: sudo karakuri service restart
    On systemd, the service install also adds karakuri-update.timer, which
    installs new releases as root while update.enabled is set in config.yaml.

    Remote hosts run the agent, which ships as its own archive and installs
    itself: karakuri-agent enroll --server <host>:9443 --token <token> --service
EOF
    exit 0
fi

# ── User install ──────────────────────────────

if [ -z "$TARGET_USER" ]; then
    TARGET_USER=$(id -un)
elif [ "$(id -u)" -ne 0 ] && [ "$TARGET_USER" != "$(id -un)" ]; then
    die "only root can install for another account"
fi

# Resolve the account's home from the password database rather than $HOME,
# which belongs to whoever is running the script and not to whoever the
# install is for.
if command -v getent >/dev/null 2>&1; then
    TARGET_HOME=$(getent passwd "$TARGET_USER" | cut -d: -f6)
else
    TARGET_HOME=$(awk -F: -v u="$TARGET_USER" '$1 == u {print $6}' /etc/passwd)
fi
[ -n "$TARGET_HOME" ] || die "no home directory for $TARGET_USER"

[ -n "$PREFIX" ] || PREFIX="$TARGET_HOME/.local"
BIN_DIR="$PREFIX/bin"
USER_DATA_DIR="$TARGET_HOME/.local/share/karakuri"
USER_CONFIG_DIR="$TARGET_HOME/.config/karakuri"

printf '==> Installing karakuri for %s from %s\n' "$TARGET_USER" "$SRC"
created=
for dir in "$TARGET_HOME/.local" "$TARGET_HOME/.local/share" "$BIN_DIR" \
           "$TARGET_HOME/.config" "$USER_DATA_DIR" "$USER_DATA_DIR/var" "$USER_CONFIG_DIR"; do
    if [ ! -d "$dir" ]; then
        mkdir -p "$dir"
        created="$created $dir"
    fi
done

printf '    installing binaries...\n'
install -m 755 "$SRC/karakuri" "$BIN_DIR/karakuri"
if [ -x "$SRC/karakuri-agent" ]; then
    install -m 755 "$SRC/karakuri-agent" "$BIN_DIR/karakuri-agent"
fi
if [ -x "$SRC/karakuri-auto" ]; then
    install -m 755 "$SRC/karakuri-auto" "$BIN_DIR/karakuri-auto"
fi

# ~/.local/share/karakuri and ~/.config/karakuri are the user tiers of the same
# search that finds /usr/share/karakuri and /etc/karakuri, so nothing here has
# to sit beside the binary and nothing has to be told where it went.
printf '    installing resources...\n'
copy_resources "$USER_DATA_DIR"
generate_config "$BIN_DIR/karakuri" "$USER_CONFIG_DIR/config.yaml"

if [ "$(id -u)" -eq 0 ]; then
    chown -R "$TARGET_USER:" "$USER_DATA_DIR" "$USER_CONFIG_DIR"
    for dir in $created; do
        chown "$TARGET_USER:" "$dir"
    done
    chown "$TARGET_USER:" "$BIN_DIR/karakuri"
    if [ -f "$BIN_DIR/karakuri-agent" ]; then
        chown "$TARGET_USER:" "$BIN_DIR/karakuri-agent"
    fi
    if [ -f "$BIN_DIR/karakuri-auto" ]; then
        chown "$TARGET_USER:" "$BIN_DIR/karakuri-auto"
    fi
fi

# The PATH being checked is the one running the script, which is the account's
# own only when it is installing for itself.
if [ "$TARGET_USER" = "$(id -un)" ]; then
    case ":$PATH:" in
        *":$BIN_DIR:"*) ;;
        *) printf '    note: %s is not on PATH\n' "$BIN_DIR" ;;
    esac
fi

cat <<EOF
==> Installation complete

    Binaries:   $BIN_DIR
    Resources:  $USER_DATA_DIR
    Config:     $USER_CONFIG_DIR/config.yaml
    Data:       $USER_DATA_DIR/var

    Run these as $TARGET_USER from a login session:
    Register the service:   karakuri service install
    Start it:               karakuri service start
    Create the first login: karakuri user create admin admin@example.com <password> --admin

    After an upgrade, restart the service: karakuri service restart

    A user service stops with the session unless the account lingers:
    sudo loginctl enable-linger $TARGET_USER
EOF
