# Phase 1 Handoff — Social-login auth hardening

**Date:** June 12, 2026
**Status:** Code complete & committed locally · **NOT pushed** · **NOT deployed**
**Commit:** `063519f` — `feat(security): Phase 1 — verify social-login tokens for real`
**Plan source:** [appFixPlan.md](appFixPlan.md) Phase 1 (items 8–12, now marked ✅)

---

## TL;DR — do this when you're back

1. **Add 3 env vars to the server** before deploying, or Google + Facebook login will break (the new code fails closed). See [§Server config](#1-server-config-required-before-deploy).
2. `git push` the commit.
3. Deploy: `docker compose -f docker-compose.prod.yml up -d --build` on the server.
4. Test each provider on a **real iOS device** (the Google audience value is the easy thing to get wrong).

---

## What changed in Phase 1 (already committed)

- **S3 — Apple tokens now cryptographically verified.** Real JWKS verification against `appleid.apple.com/auth/keys` (cached RSA keyset, RS256-only keyfunc, then iss/aud/exp). Was `ParseUnverified` = anyone could forge a token for any email → full takeover.
- **S4 — Google/Facebook hardened.** Google: `GOOGLE_CLIENT_ID` audience now always enforced + issuer check. Facebook: `debug_token` requires `is_valid && app_id == FACEBOOK_APP_ID`. Both fail closed.
- **Account linking reworked.** Only auto-links a social identity to an existing email account when the provider asserts the email is verified; otherwise returns **HTTP 409 `link_requires_verification`** with no JWT.
- **B3 — signup restore query fixed** (grouped-OR binds `restored_at IS NULL` to every branch) and no longer leaks `original_username`/`history_id`/`deleted_at`.
- **S10 — content-service** now pins the HMAC signing method in `jwt.Parse`.
- Tests: `auth-service/apple_verify_test.go` (6 cases, all pass). Run with `JWT_SECRET=test-secret go -C auth-service test ./...`.

Files: `auth-service/main.go`, `content-service/main.go`, `auth-service/apple_verify_test.go`, `.env.example`, `appFixPlan.md`.

---

## 1. Server config — REALITY (checked against the iOS app, June 15 2026)

**Only Apple Sign-In is wired in the iOS app.** Google and Facebook are stubs
in `AudioBook/Services/SocialAuthService.swift` (`signInWithGoogle` /
`signInWithFacebook` immediately return `.notConfigured` — no SDK, no
GoogleService-Info.plist, empty `Info.plist` URL schemes). So the app cannot
produce Google/Facebook tokens yet; their server env vars are NOT needed to
deploy and can't be tested until the iOS SDKs are integrated.

**Real iOS bundle ID:** `com.rmhrealestate.AudioBook` (from `AudioBook.xcodeproj`).

**Server:** `ssh stream-app` → deploy dir `/opt/stream-audio` → env file `/opt/stream-audio/.env`
(loaded via `env_file: .env` in `docker-compose.prod.yml`).

| Var | On server? | Needed for this deploy? |
|-----|-----------|------------------------|
| `APPLE_BUNDLE_ID` | ❌ missing | **Recommended.** Code default now `com.rmhrealestate.AudioBook` (fixed in commit), so Apple works even if unset; set it explicitly for clarity. |
| `GOOGLE_CLIENT_ID` | ❌ missing | No — Google not wired in app. Set when integrating GoogleSignIn SDK. |
| `FACEBOOK_APP_ID` / `FACEBOOK_APP_SECRET` | ❌ missing | No — Facebook not wired in app. Set when integrating FB SDK. |
| `JWT_SECRET` | ✅ set (64 chars) | — |

**Action (optional but recommended) — append to `/opt/stream-audio/.env`:**
```bash
APPLE_BUNDLE_ID=com.rmhrealestate.AudioBook
```

### ⚠️ Verify the Apple capability
`AudioBook.entitlements` currently shows only `inter-app-audio`, NOT
`com.apple.developer.applesignin`. Confirm "Sign in with Apple" is enabled on
the App ID (Apple Developer portal) + in Xcode Signing & Capabilities, or the
native flow won't return a usable token regardless of backend.

### Apple notes
- Needs **outbound HTTPS to `appleid.apple.com`** for JWKS (Phase 0 only closed
  inbound ports; Docker keeps outbound by default — confirm no egress firewall).

### Future: getting Google / Facebook credentials
- **GOOGLE_CLIENT_ID** — Google Cloud Console → Credentials → OAuth client ID →
  type **iOS**, bundle `com.rmhrealestate.AudioBook`. The **iOS client ID**
  (`…apps.googleusercontent.com`) is the token `aud` → that's the server value.
  Reversed client ID goes in iOS Info.plist URL schemes.
- **FACEBOOK_APP_ID / _SECRET** — developers.facebook.com → Create App (Consumer,
  Facebook Login). App ID = dashboard top; App Secret = Settings → Basic (server
  only). Add iOS platform with the bundle ID.

---

## 2. Deploy steps
```bash
# local
git push                      # commit 063519f → main  (remote/gh access unverified — may need setup)

# on server (ssh stream-app)
cd /opt/stream-audio
# edit .env to add the 3 vars above
docker compose -f docker-compose.prod.yml up -d --build auth-service content-service
docker compose -f docker-compose.prod.yml logs -f auth-service   # watch startup
```
On startup, auth-service now prints a `⚠️` line for any provider whose vars are missing — quick confirmation the env is wired.

---

## 3. Verify after deploy
- **Apple** sign-in on a real device → succeeds.
- **Google** sign-in on a real device → succeeds (this is the one to watch — wrong client ID = rejection).
- **Facebook** login on a real device → succeeds.
- Sanity: a forged/garbage Apple token → 401 (the S3 fix).

---

## 4. Known follow-up (not blocking deploy)
- **iOS 409 handling:** the new `link_requires_verification` (HTTP 409) response isn't handled gracefully by the app yet — should show "sign in with your password to link this provider" instead of a generic error. Small iOS task.
- **In-repo documentation:** the social vars are only in the server's untracked `.env`. Optional: wire them into the `environment:` block of `docker-compose.prod.yml` so they're documented in the repo (offered, not yet done).

---

## 5. Next: Phase 2 — Ownership & account lifecycle (2–3 days)
From [appFixPlan.md](appFixPlan.md) Phase 2 (items 13–17):
- **S6** — `requireBookOwnership()` middleware closing IDOR across nearly every book endpoint (today any logged-in user can read/stream/transcribe/delete *anyone's* books).
- **S7** — upload path-traversal + cross-user overwrite hardening (ignore client filename, save under `{user_id}/{book_id}/`, sniff content type).
- **B2** — fix admin-deletion column bugs (`user_id` vs `original_user_id`) + add an `audit_log`.
- **S5** — redesign `/restore-account` (currently disabled/410) with real proof of identity, then re-enable.
- **Q11** — cascade book deletion + reset chunks on re-upload.

**S6 is the biggest remaining hole** — likely the place to start Phase 2.
