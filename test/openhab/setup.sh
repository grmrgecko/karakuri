#!/bin/sh
# Creates the test admin and an API token in the running test openHAB through
# its Karaf console, and writes the token to build/openhab-token. The console
# ignores commands passed as arguments, so they go in on stdin, and it answers
# for a while after the REST API is up with no session, so each step retries.
set -eu
compose="env OPENHAB_UID=$(id -u) OPENHAB_GID=$(id -g) docker compose -f test/openhab/compose.yaml -p karakuri-openhab"

consoled() {
	printf '%s\nlogout\n' "$1" |
		$compose exec -T openhab /openhab/runtime/bin/client -h 127.0.0.1 -u openhab -p habopen 2>&1 |
		sed 's/\x1b\[[0-9;]*[a-zA-Z]//g'
}

tries=0
until consoled 'openhab:users list' | grep -q 'admin (administrator)'; do
	consoled 'openhab:users add admin karakuri-test administrator' >/dev/null || true
	tries=$((tries + 1))
	if [ "$tries" -ge 40 ]; then
		printf 'the openHAB console never created the admin user\n' >&2
		exit 1
	fi
	sleep 3
done

token=$(consoled 'openhab:users addApiToken admin karakuri items' | grep -o 'oh\.karakuri\.[A-Za-z0-9]*' | tail -n 1)
if [ -z "$token" ]; then
	printf 'the openHAB console gave no API token\n' >&2
	exit 1
fi
mkdir -p build
umask 077
printf '%s\n' "$token" > build/openhab-token
printf 'openHAB test token written to build/openhab-token\n'
