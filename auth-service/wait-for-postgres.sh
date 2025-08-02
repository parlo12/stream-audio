#!/bin/sh
set -e

host="$DB_HOST"
port="${DB_PORT:-5432}"

echo "Waiting for Postgres at $host:$port..."

while ! nc -z "$host" "$port"; do
  sleep 1
done

echo "Postgres is ready. Starting service..."
exec "$@"