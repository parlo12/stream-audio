# nginx hardening (production edge)

In production, **nginx on the host** (not the Go gateway) is the edge proxy:
`https://narrafied.com` → `localhost:8082` (auth) / `localhost:8083` (content).
Site config lives at `/etc/nginx/sites-available/stream-audio`.

Applied June 15, 2026 (Phase 5):

1. **Per-IP rate limiting** on the auth endpoints. Zone defined in
   `conf.d-ratelimit.conf` (deployed to `/etc/nginx/conf.d/ratelimit.conf`):
   `rate=10r/m`, `burst=5 nodelay`, `429` on exceed. Applied to the `/login`,
   `/signup`, and `/auth/` locations via:
   ```nginx
   limit_req zone=auth_limit burst=5 nodelay;
   ```
2. **Request correlation**: server-level `add_header X-Request-ID $request_id always;`
   plus `proxy_set_header X-Request-ID $request_id;` forwarded to upstreams.
3. **New `/auth/` location** → `localhost:8082` (social login had no nginx route
   before; it now proxies + is rate-limited).
4. `client_max_body_size 500M` retained (audiobook uploads).

## Verified
- `/health` → 200; `X-Request-ID` present on responses.
- 10 rapid `POST /login` → 6× 401 then 4× 429 (burst then throttle).
- `/user/books` (no token) → 401, not rate-limited.

## Apply / change procedure
1. `cp /etc/nginx/sites-available/stream-audio /root/nginx-backup-$(date +%F-%H%M%S)/`
2. Edit the site / `conf.d/ratelimit.conf`.
3. `nginx -t` (must pass).
4. `systemctl reload nginx`; verify `/health` and a `429` on burst.
5. Roll back by restoring the backup + `systemctl reload nginx`.

Tune the rate via the `rate=`/`burst=` values; raise if legitimate users hit 429.
