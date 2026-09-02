# Promotional-Pricing Offer Engine (web) — Runbook

Web-only (Stripe). iOS uses Apple's own offer types and is deferred. Shipped in
auth-service: `email.go`, `offers.go`, plus wiring in `main.go`.

## What it does
- New web checkouts bill the **anchor** price ($18.99 premium) via
  `STRIPE_PRICE_PREMIUM_ANCHOR`.
- A registered user who hasn't subscribed within **24h** (and ≤72h old) gets one
  **abandon** email offering the **floor** price ($14.99).
- A subscriber who cancels gets, **24h** later, one **winback** email at the floor.
- Emails link to `/offer/:token` → starts a Stripe Checkout at the floor price for
  that user. `/unsubscribe/:token` sets `marketing_opt_out` (CAN-SPAM).
- The floor ($14.99) is the margin-validated price; the discount never dips below it.

## Safety switches (inert until all are set)
- `OFFER_EMAILS_ENABLED` — loop is OFF unless this is exactly `true`.
- SES unconfigured → `sendEmail` no-ops with a log line.
- `STRIPE_PRICE_PREMIUM_ANCHOR` unset → checkout falls back to the current
  `STRIPE_PRICE_ID` ($14.99), i.e. no behavior change.
So deploying the code changes nothing until you complete activation below.

## Activation checklist

### 1. Amazon SES (AWS console — your action)
- Verify the sending domain `narrafied.com` (add the DKIM CNAMEs SES gives you).
- Move the account out of the SES sandbox (request production access) so it can
  email non-verified recipients.
- Create **SMTP credentials** (SES → SMTP settings → Create). These are NOT your
  AWS access keys.

### 2. Stripe anchor prices (on the droplet)
    ssh stream-app 'bash -s' < deploy/stripe-anchor-prices.sh
Copy the printed price IDs.

### 3. Env (/opt/stream-audio/stream-audio/.env)
    SES_SMTP_HOST=email-smtp.us-east-1.amazonaws.com   # your region
    SES_SMTP_USER=<SES SMTP username>
    SES_SMTP_PASS=<SES SMTP password>
    SES_SENDER=Narrafied <hello@narrafied.com>          # must be on the verified domain
    STRIPE_PRICE_PREMIUM_ANCHOR=<from step 2, $18.99>
    STRIPE_PRICE_PREMIUM=price_1U8WGRChBqCooXQKF3FL3bXS # $14.99 floor
    OFFER_BASE_URL=https://narrafied.com
    OFFER_ANCHOR_PRICE_DISPLAY=$18.99
    OFFER_DISCOUNT_PRICE_DISPLAY=$14.99
    OFFER_EMAILS_ENABLED=true                            # flip LAST, after testing

### 4. nginx — expose the two public routes on auth-service (8082)
Add to the narrafied.com server block (both must proxy to 8082, like /invite):
    location /offer/       { proxy_pass http://127.0.0.1:8082; }
    location /unsubscribe/ { proxy_pass http://127.0.0.1:8082; }
Then: `nginx -t && systemctl reload nginx`

### 5. Deploy
    ssh stream-app 'cd /opt/stream-audio/stream-audio && git pull && \
      docker compose -f docker-compose.prod.yml up -d --build auth-service'
AutoMigrate adds the `offer_emails` table and the `subscription_canceled_at` /
`marketing_opt_out` columns automatically.

### 6. Test before flipping the switch
- Leave `OFFER_EMAILS_ENABLED` unset; hit `/offer/<token>` with a token you mint
  to confirm the discounted checkout opens.
- Send yourself a test by temporarily lowering the window or seeding a row.
- Then set `OFFER_EMAILS_ENABLED=true` and recreate auth-service.

## Compliance notes
- The anchor is a REAL price genuinely charged — this is segmented/promotional
  pricing, not a fictitious "was" price. Do NOT render a fake strikethrough
  "$18.99 → $14.99" claiming a former price; the honest framing (already in the
  email copy) is "our standard rate, below the price you saw."
- Every promo email carries an unsubscribe link; opt-outs are honored by the loop.
- Before scale, add your physical mailing address to the email footer (CAN-SPAM).

## Tuning
- `OFFER_*_DISPLAY` control the copy only; the actual charged prices come from the
  Stripe price IDs. Keep them in sync.
- Windows (24h ready / 72h lookback) and the 200/pass cap are in
  `runOfferEmailPass` (offers.go).
