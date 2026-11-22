#!/bin/sh
# Agent container entrypoint. Enrolls on first start from the environment, then
# execs karakuri-agent with whatever the command line asked for.
#
#   KARAKURI_SERVER      host:port of the server's agent listener. Required to
#                        enroll; afterwards it overrides the enrolled address.
#   KARAKURI_JOIN_TOKEN  join token from the agents panel. Read only when the
#                        identity volume holds no identity yet.
#   KARAKURI_AGENT_NAME  preferred agent name; the container hostname otherwise.
#
# The identity lives on the /var/lib/karakuri volume, so a recreated container
# keeps its agent instead of enrolling as a new one with a spent token.
set -eu

DIR=/var/lib/karakuri/agent

# Only `run` needs an identity; `version` or `credential list` should work on
# an empty volume.
if [ "${1:-}" = "run" ]; then
    if [ ! -f "$DIR/agent.json" ]; then
        if [ -z "${KARAKURI_JOIN_TOKEN:-}" ] || [ -z "${KARAKURI_SERVER:-}" ]; then
            echo "karakuri-agent: not enrolled; set KARAKURI_SERVER and KARAKURI_JOIN_TOKEN for the first start" >&2
            exit 1
        fi
        echo "karakuri-agent: enrolling with $KARAKURI_SERVER"
        karakuri-agent enroll --server "$KARAKURI_SERVER" --token "$KARAKURI_JOIN_TOKEN" \
            --name "${KARAKURI_AGENT_NAME:-$(hostname)}"
    fi
    if [ -n "${KARAKURI_SERVER:-}" ]; then
        shift
        set -- run --server "$KARAKURI_SERVER" "$@"
    fi
fi

exec karakuri-agent "$@"
