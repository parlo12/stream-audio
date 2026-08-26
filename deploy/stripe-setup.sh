#!/bin/bash
# Narrafied Stripe setup — creates Starter/Premium products + 4 recurring prices.
# Idempotent: safe to re-run; existing lookup_keys are reused, nothing is deleted.
# Runs ON the droplet so the secret key never leaves the server. Prints no secrets.
set -euo pipefail
cd /opt/stream-audio/stream-audio
set -a; . ./.env; set +a

api() { curl -s -u "$STRIPE_SECRET_KEY:" "$@"; }

# 0. Safety: confirm we're on the expected account
ACCT=$(api https://api.stripe.com/v1/account | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")
if [ "$ACCT" != "acct_1RpzRLChBqCooXQK" ]; then
  echo "ABORT: key belongs to '$ACCT', expected acct_1RpzRLChBqCooXQK"; exit 1
fi
echo "✓ account verified: $ACCT"

# 1. Find-or-create a product by exact name
get_or_create_product() { # $1=name $2=description
  local id
  id=$(api "https://api.stripe.com/v1/products?limit=100&active=true" | python3 -c "
import json,sys
for p in json.load(sys.stdin)['data']:
    if p['name'] == '$1': print(p['id']); break")
  if [ -z "$id" ]; then
    id=$(api https://api.stripe.com/v1/products \
      -d "name=$1" -d "description=$2" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
    echo "  created product: $1 ($id)" >&2
  else
    echo "  exists product:  $1 ($id)" >&2
  fi
  echo "$id"
}

# 2. Find-or-create a recurring price by lookup_key
get_or_create_price() { # $1=product $2=lookup_key $3=amount_cents $4=interval
  local id
  id=$(api "https://api.stripe.com/v1/prices?lookup_keys[]=$2&active=true" | python3 -c "
import json,sys
d=json.load(sys.stdin)['data']
print(d[0]['id'] if d else '')")
  if [ -z "$id" ]; then
    id=$(api https://api.stripe.com/v1/prices \
      -d "product=$1" -d "lookup_key=$2" -d "currency=usd" \
      -d "unit_amount=$3" -d "recurring[interval]=$4" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
    echo "  created price:   $2 -> $id" >&2
  else
    echo "  exists price:    $2 -> $id" >&2
  fi
  echo "$id"
}

STARTER=$(get_or_create_product "Narrafied Starter" "2 hours of new audiobook transcription per month, unlimited replays")
PREMIUM=$(get_or_create_product "Narrafied Premium" "8 hours of new audiobook transcription per month, unlimited replays")

SM=$(get_or_create_price "$STARTER" narrafied_starter_monthly 799   month)
SY=$(get_or_create_price "$STARTER" narrafied_starter_yearly  7999  year)
PM=$(get_or_create_price "$PREMIUM" narrafied_premium_monthly 1499  month)
PY=$(get_or_create_price "$PREMIUM" narrafied_premium_yearly  14999 year)

echo
echo "=== RESULT (save these) ==="
echo "starter monthly  \$7.99   $SM"
echo "starter yearly   \$79.99  $SY"
echo "premium monthly  \$14.99  $PM"
echo "premium yearly   \$149.99 $PY"
echo
echo "Current STRIPE_PRICE_ID in .env (old \$24.99 — leave until code-sync deploy):"
grep -E "^STRIPE_PRICE_ID=" .env || echo "  (not set)"
echo
echo "Old active recurring prices on the account (deactivate AFTER the code-sync deploy):"
api "https://api.stripe.com/v1/prices?active=true&type=recurring&limit=100" | python3 -c "
import json,sys
keep={'$SM','$SY','$PM','$PY'}
for p in json.load(sys.stdin)['data']:
    if p['id'] not in keep:
        print(f\"  {p['id']}  \${p['unit_amount']/100:.2f}/{p['recurring']['interval']}  product={p['product']}\")"
