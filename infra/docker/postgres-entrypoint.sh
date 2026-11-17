#!/bin/sh
set -eu

# Reconcile database roles in the background once PostgreSQL accepts connections.
# This ensures roles (such as bliss_catalog) are automatically migrated on existing
# persisted volumes where docker-entrypoint-initdb.d scripts are skipped by PostgreSQL.
# For fresh volumes, docker-entrypoint-initdb.d is executed directly by docker-entrypoint.sh.
if [ -f /var/lib/postgresql/data/PG_VERSION ]; then
    (
        while ! pg_isready -U "${POSTGRES_USER:-bliss_admin}" -d "${POSTGRES_DB:-bliss_dev}" -q 2>/dev/null; do
            sleep 0.2
        done
        if [ -f /docker-entrypoint-initdb.d/10-roles.sh ]; then
            sh /docker-entrypoint-initdb.d/10-roles.sh >/dev/null 2>&1 || true
        fi
    ) &
fi

exec docker-entrypoint.sh "$@"
