#!/bin/sh
set -eu

if [ -f /run/secrets/rabbitmq_password ]; then
    export RABBITMQ_DEFAULT_PASS="$(cat /run/secrets/rabbitmq_password)"
fi

exec /usr/local/bin/docker-entrypoint.sh "$@"
