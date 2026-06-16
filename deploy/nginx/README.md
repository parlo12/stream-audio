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

Added June 16, 2026:

5. **Content-service `/user/*` exceptions.** `/user/` falls through to auth
   (`8082`) by default; content-service `/user/*` endpoints each need their own
   `location` → `8083`, or they 404 on auth. Existing: `/user/books`,
   `/user/chunks`, `/user/progress`, `/user/search-books`, `/user/stats/`,
   `/user/search-book-covers`. **Added** `/user/device-token` (APNs registration)
   and `/user/bug-report` (in-app bug reports) → `localhost:8083`.
   ⚠️ Any NEW content-service route under `/user/` must get an nginx location
   block here too, or it will 404 (it hits auth-service instead).
6. **`/admin/grafana/`** → `localhost:3000` (Grafana, sub-path) and
   **`/admin/queues/`** → `localhost:8085` (asynqmon), both behind admin auth.

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

## asynq queue dashboard (Phase 5A)

The `asynqmon` container (compose) binds `127.0.0.1:8085`. nginx fronts it at
`/admin/queues/` behind HTTP basic auth:
```nginx
location /admin/queues/ {
    auth_basic "Restricted";
    auth_basic_user_file /etc/nginx/.queues_htpasswd;
    proxy_pass http://localhost:8085/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```
Credentials live in `/etc/nginx/.queues_htpasswd` (created via
`openssl passwd -apr1`); rotate with that file + `systemctl reload nginx`.
