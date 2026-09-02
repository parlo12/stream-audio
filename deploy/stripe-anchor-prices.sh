#!/bin/bash
# Create the ANCHOR monthly prices for the promotional-pricing engine:
#   Starter anchor  $9.99   (floor stays $7.99)
#   Premium anchor  $18.99  (floor stays $14.99)
# Idempotent (find-or-create by lookup_key). Runs on the droplet so the Stripe
# secret key never leaves the server. Prints the env lines to add to .env.
set -euo pipefail
cd /opt/stream-audio/stream-audio
set -a; . ./.env; set +a
api() { curl -s -u "$STRIPE_SECRET_KEY:" "$@"; }

ACCT=$(api https://api.stripe.com/v1/account | python3 -c "import json,sys;print(json.load(sys.stdin).get('id',''))")
[ "$ACCT" = "acct_1RpzRLChBqCooXQK" ] || { echo "ABORT: wrong account $ACCT"; exit 1; }
echo "✓ account $ACCT"

# reuse existing products (created by stripe-setup.sh)
prod() { api "https://api.stripe.com/v1/products?limit=100&active=true" | python3 -c "
import json,sys
for p in json.load(sys.stdin)['data']:
    if p['name']=='$1': print(p['id']); break"; }

price() { # $1=product $2=lookup_key $3=cents
  local id
  id=$(api "https://api.stripe.com/v1/prices?lookup_keys[]=$2&active=true" | python3 -c "
import json,sys; d=json.load(sys.stdin)['data']; print(d[0]['id'] if d else '')")
  if [ -z "$id" ]; then
    id=$(api https://api.stripe.com/v1/prices -d "product=$1" -d "lookup_key=$2" \
      -d currency=usd -d "unit_amount=$3" -d "recurring[interval]=month" \
      | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
    echo "  created $2 -> $id" >&2
  else echo "  exists  $2 -> $id" >&2; fi
  echo "$id"
}

STARTER=$(prod "Narrafied Starter"); PREMIUM=$(prod "Narrafied Premium")
SA=$(price "$STARTER" narrafied_starter_anchor 999)
PA=$(price "$PREMIUM" narrafied_premium_anchor 1899)

echo
echo "=== add these to /opt/stream-audio/stream-audio/.env ==="
echo "STRIPE_PRICE_PREMIUM_ANCHOR=$PA        # \$18.99 shown to new signups"
echo "STRIPE_PRICE_PREMIUM=price_1U8WGRChBqCooXQKF3FL3bXS   # \$14.99 floor (offer links)"
echo "STRIPE_PRICE_STARTER_ANCHOR=$SA        # \$9.99 (starter, when web sells it)"
echo "# starter floor already exists: price_1U8WGQChBqCooXQKkldodFhV  (\$7.99)"
