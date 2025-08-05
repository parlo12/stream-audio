#!/bin/sh
set -e

host="${DB_HOST}"
port="${DB_PORT:-5432}"
user="${DB_USER}"
password="${DB_PASSWORD}"
dbname="${DB_NAME}"

sslmode="${DB_SSLMODE:-disable}"


export PGPASSWORD="$password"

echo "🔄 Waiting for Postgres at $host:$port (user: $user, sslmode=$sslmode)..."

attempt=0
while ! psql "host=$host port=$port user=$user dbname=$db sslmode=$sslmode sslrootcert=/certs/do-postgres-ca.crt" -c '\q' 2>/dev/null; do
  attempt=$((attempt+1))
  echo "Attempt $attempt: Postgres is unavailable – sleeping"
  sleep 2
done

echo "✅ Postgres is ready. Starting service..."
unset PGPASSWORD

exec "$@"