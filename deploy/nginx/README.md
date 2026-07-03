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

Added June 17, 2026 (Phase A — remote config):

7. **`/user/config`** (content-service remote config: feature flags, copy,
   colors, displayed pricing, min-supported-build version gate). Like every
   content-service `/user/*` route it needs its own location → `8083` or it
   404s on auth-service. Add:
   ```nginx
   location /user/config {
       proxy_pass http://localhost:8083;
       proxy_set_header Host $host;
       proxy_set_header Authorization $http_authorization;
       proxy_set_header X-Real-IP $remote_addr;
       proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
       proxy_set_header X-Forwarded-Proto $scheme;
   }
   ```

Added June 18, 2026 (Connect/casting Phase 1):

8. **`/user/cast-events`** (content-service: records AirPlay/Bluetooth/Chromecast
   cast events). Same `/user/*` → `8083` rule:
   ```nginx
   location /user/cast-events {
       proxy_pass http://localhost:8083;
       proxy_set_header Host $host;
       proxy_set_header X-Real-IP $remote_addr;
       proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
       proxy_set_header X-Forwarded-Proto $scheme;
   }
   ```

Added July 3, 2026 (referral program):

8b. **`/invite/`** → `localhost:8082` (auth-service `GET /invite/:code` — public
    referral-link redirect to `INVITE_REDIRECT_URL`). Without this block the
    static-site `location /` swallows invite links and 404s:
    ```nginx
    location /invite/ {
        proxy_pass http://localhost:8082;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Request-ID $request_id;
    }
    ```
    The other referral endpoints (`/user/referral`,
    `/user/subscription/validate-receipt`) ride the existing `/user/` default
    → auth-service; no new blocks needed.

Added July 3, 2026 (raw-IP exposure fix):

9. **Default catch-all vhost** `sites-available/default-drop` (enabled in
   `sites-enabled/`). Scanners hitting the raw IP were being served the Admin
   Dashboard, because the old `admin` vhost (`listen 80; server_name
   68.183.22.205;`) loaded alphabetically before `stream-audio` and acted as
   the de facto default for port 80. Now any request that doesn't match a
   real `server_name` is dropped:
   ```nginx
   server {
       listen 80 default_server;
       listen [::]:80 default_server;
       server_name _;
       return 444;
   }

   server {
       listen 443 ssl default_server;
       listen [::]:443 ssl default_server;
       server_name _;
       ssl_reject_handshake on;   # nginx >= 1.19.4
   }
   ```
   The `admin` vhost is disabled (symlink removed from `sites-enabled/`; file
   kept in `sites-available/` for reference).

10. **Admin Dashboard moved to `https://narrafied.com/dashboard/`** (static
    files still at `/var/www/admin`), behind the same basic auth as asynqmon:
    ```nginx
    location = /dashboard {
        return 301 /dashboard/;
    }

    location /dashboard/ {
        auth_basic "Restricted";
        auth_basic_user_file /etc/nginx/.queues_htpasswd;
        alias /var/www/admin/;
        index index.html;
        try_files $uri $uri/ /dashboard/index.html;
    }

    # Dashboard API base: strip /api and forward to auth-service
    location /api/ {
        proxy_pass http://localhost:8082/;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Request-ID $request_id;
    }
    ```
    ⚠️ `/api/` must NOT get `auth_basic`: the dashboard sends
    `Authorization: Bearer <JWT>` on those calls, which collides with basic
    auth's use of the same header. The `/api/*` admin routes are already
    protected by the services' own `is_admin` JWT checks (and were already
    publicly routable via `/admin/` on this vhost anyway).

## Verified
- `/health` → 200; `X-Request-ID` present on responses.
- 10 rapid `POST /login` → 6× 401 then 4× 429 (burst then throttle).
- `/user/books` (no token) → 401, not rate-limited.
- (July 3, 2026) `http://68.183.22.205/` → connection dropped (444);
  `https://68.183.22.205/` → TLS handshake rejected; `narrafied.com` site,
  `/health`, `/user/config` (401), `/login` (400) all intact;
  `/dashboard/` → 401 without credentials, serves index/app.js/styles.css
  and SPA fallback with credentials; `/api/health` → 200.

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
