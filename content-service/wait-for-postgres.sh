#!/bin/sh
set -e

host="${DB_HOST}"
port="${DB_PORT:-5432}"
user="${DB_USER}"
password="${DB_PASSWORD}"
dbname="${DB_NAME}"

export PGPASSWORD="$password"

echo "🔄 Waiting for Postgres at $host:$port (user: $user)..."

attempt=0
while true; do
  if psql "host=$host port=$port user=$user dbname=$dbname sslmode=require" -c '\q'; then
    echo "✅ Postgres is ready. Starting service..."
    break
  else
    attempt=$((attempt+1))
    echo "Attempt $attempt: Postgres is unavailable - sleeping"
    sleep 2
  fi
done

# Debug: confirm what will be executed

echo "🚀 Launching app: $@"
echo "----------------------------------------"

unset PGPASSWORD
exec "$@"