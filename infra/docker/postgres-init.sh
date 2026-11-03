#!/bin/sh
set -eu
BLISS_MIGRATOR_PASSWORD=$(cat /run/secrets/migrator_password)
BLISS_RUNTIME_PASSWORD=$(cat /run/secrets/runtime_password)
BLISS_CATALOG_PASSWORD=$(cat /run/secrets/catalog_password)
export BLISS_MIGRATOR_PASSWORD BLISS_RUNTIME_PASSWORD BLISS_CATALOG_PASSWORD
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
\getenv migrator_password BLISS_MIGRATOR_PASSWORD
\getenv runtime_password BLISS_RUNTIME_PASSWORD
\getenv catalog_password BLISS_CATALOG_PASSWORD
SET custom.migrator_password = :'migrator_password';
SET custom.runtime_password = :'runtime_password';
SET custom.catalog_password = :'catalog_password';
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'bliss_migrator') THEN
    EXECUTE format('CREATE ROLE bliss_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L', current_setting('custom.migrator_password'));
  ELSE
    EXECUTE format('ALTER ROLE bliss_migrator WITH PASSWORD %L', current_setting('custom.migrator_password'));
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
    EXECUTE format('CREATE ROLE bliss_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L', current_setting('custom.runtime_password'));
  ELSE
    EXECUTE format('ALTER ROLE bliss_runtime WITH PASSWORD %L', current_setting('custom.runtime_password'));
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'bliss_catalog') THEN
    EXECUTE format('CREATE ROLE bliss_catalog LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L', current_setting('custom.catalog_password'));
  ELSE
    EXECUTE format('ALTER ROLE bliss_catalog WITH PASSWORD %L', current_setting('custom.catalog_password'));
  END IF;
END $$;
ALTER DATABASE bliss_dev OWNER TO bliss_migrator;
REVOKE ALL ON DATABASE bliss_dev FROM PUBLIC;
GRANT CONNECT ON DATABASE bliss_dev TO bliss_runtime, bliss_catalog;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
SQL
unset BLISS_MIGRATOR_PASSWORD BLISS_RUNTIME_PASSWORD BLISS_CATALOG_PASSWORD
