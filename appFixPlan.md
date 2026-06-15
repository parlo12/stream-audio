# Narrafied — App Fix Plan

**Date:** June 11, 2026
**Scope:** Every issue found in a full read of the backend (`stream-audio`, all ~9,000 lines) and a targeted audit of the iOS app (`AudioBook`), ordered into fix phases. Architecture evolution (object storage, queues, presigned uploads, quotas) lives in [structureImprovmentPlan.md](structureImprovmentPlan.md) — **this plan is the prerequisite work that makes that migration safe.**

Severity legend: **P0** = exploitable security hole or feature broken in production · **P1** = correctness/cost/quality bug · **P2** = architecture & hygiene debt.

---

## Part A — Issue Inventory

### A1. Security — exposure & access control (P0)

| # | Issue | Evidence |
|---|---|---|
| S1 | **All generated audio and covers are publicly downloadable with no auth.** `router.Static("/audio", "./audio")` and `router.Static("/covers", ...)` serve every file to anyone; combined with S2 the entire paid product is free to anyone who guesses/observes a filename (filenames are predictable: `book_{id}_page_{n}_{hash8}.mp3`). | content-service/main.go:128-131 |
| S2 | **Internal services are published to the internet.** Prod compose maps `8082:8082`, `8083:8083`, **and Redis `6379:6379`** to the host. Auth, content, and an unauthenticated Redis are all reachable directly, bypassing nginx/gateway entirely. Unprotected Redis = trivially writable/wipeable by anyone scanning the IP. | docker-compose.prod.yml:9,48,95-97 |
| S3 | **Apple Sign-In tokens are never signature-verified.** `verifyAppleToken` uses `jwt.ParseUnverified` and only checks iss/aud/exp strings — anyone can mint a "valid" Apple token. Because `handleSocialLogin` links by email, a forged token with a victim's email = **full takeover of any account**. | auth-service/main.go:2211-2245, 2337-2363 |
| S4 | **Google/Facebook verification is incomplete.** Google: audience check skipped when `GOOGLE_CLIENT_ID` is unset (tokens minted for *any* app accepted). Facebook: token verified via `/me` only — no `appsecret_proof` / `debug_token` check that the token was issued **for your app**, so any other FB app's user token logs into Narrafied. Both link by email → same takeover path as S3. | auth-service/main.go:2247-2306 |
| S5 | **Public `/restore-account` issues a JWT from an email alone.** No password, no proof of identity — anyone who knows that a deleted account's email can resurrect the account *and receive a logged-in token* (90-day window). It even restores `AccountType: paid` without checking Stripe. | auth-service/main.go:1146-1276 |
| S6 | **IDOR on nearly every book endpoint.** `DELETE /books/:id`, `GET /books/:id` (returns full text), `GET /books/:id/chunks/pages`, `POST /books/upload` (book_id form field), `POST /books/:id/tts/batch`, `GET .../pages/:page/audio`, `GET .../chunks/:s/:e/audio`, `GET /chunks/tts/merged-audio/:id` — **none verify `book.UserID == token user_id`.** Any logged-in user can read, stream, transcribe (spending your API credits), overwrite, or delete any other user's books. Only `proxyBookAudioHandler` and the progress handlers check ownership. | content-service/main.go:335-353, 357-435, 753-785; fileupload.go:25-105; audio_stream.go (both handlers); stream_chunk_group.go |
| S7 | **Upload path traversal + cross-user overwrite.** `dest := filepath.Join("./uploads", file.Filename)` uses the client-supplied filename: `../` escapes the uploads dir (arbitrary file write limited to allowed extensions), and two users uploading `book.pdf` silently overwrite each other (then dedup/chunking reads the wrong user's content). | content-service/fileupload.go:77 |
| S8 | **JWT secret falls back to hardcoded guessable defaults, and they differ per service** (`"your_secret_key"` vs `"defaultSecrete"`). A deploy missing the env var silently issues forgeable tokens. | auth-service/main.go:32; content-service/main.go:27 |
| S9 | **Secrets and tokens are logged.** Full DB DSN including password at startup in both services; full JWT printed on every stream request. | auth-service/main.go:358; content-service/main.go:242; streaming.go:21 |
| S10 | **Admin destructive endpoints under-protected.** `POST /admin/system/wipe` "confirmation" is a constant string in the source; no audit log on any admin mutation; no protection against a leaked admin JWT (72 h lifetime, no revocation). Also content-service's `authMiddleware` doesn't pin the HMAC signing method like auth-service does. | auth-service/main.go:1519-1643; content-service/main.go:536-538 |
| S11 | **Gateway/admin routing mismatch.** Gateway sends `/admin/*` to **content**-service only, so auth-service's admin API (stats/users/wipe) is unreachable via the gateway and "works" only because port 8082 is publicly exposed (S2). Closing S2 without fixing routing breaks the admin dashboard. | gateway/main.go:46; auth-service/main.go:296 |

### A2. Broken features (P0 correctness)

| # | Issue | Evidence |
|---|---|---|
| B1 | **Account deactivation, deletion, and activity-ping always return 401.** Handlers read `c.Get("user_id")`, but auth-service's middleware only ever sets `"claims"`. The App-Store-required deletion flow is dead code (content-service sets both keys; auth-service was never updated). | auth-service/main.go:744 vs 928, 1036, 1320 |
| B2 | **Admin user-data deletion deletes nothing / errors.** `deleteUserDataHandler`/`deleteUserCompleteHandler` filter `UserHistory` by `user_id = ?` — that column doesn't exist (it's `original_user_id`); UserBookHistory similarly keyed by `user_history_id`. | auth-service/main.go:1734-1745, 1823-1826 |
| B3 | **Signup restore-detection query is wrong and leaks data.** `.Or("phone_number = ?")`/`.Or("device_id = ?")` sit outside the `restored_at IS NULL` filter, so already-restored or *other people's* history rows match — blocking signups and returning the **other user's** username, history_id, and deletion date in the response. | auth-service/main.go:398-424 |
| B4 | **Concurrent transcription corrupts audio (latent today, guaranteed under parallelism).** Fixed shared paths: `./dyn_seg_%d.ogg`, `./dyn_crossfade_%d.ogg`, `./audio/dynamic_background_final.ogg`, `./audio/sound_effect.mp3` (every background-music call without an id), `./audio/concat_list.txt`. Worse, `cleanupTempFiles` *globs and deletes these patterns globally*, killing other in-flight jobs' files. | sound_effects.go:326,355,370,391,131,1066-1085; tts_processing.go:400 |
| B5 | **`effectCache` is a plain map written from concurrent goroutines** → Go runtime panic ("concurrent map writes") under simultaneous Foley generation, crashing the whole service. | sound_effects.go:42,925 |
| B6 | **Free-tier limit is a one-shot check before unbounded work.** Check runs once (`completedChunks >= 1`), then the goroutine transcribes the *entire book*. A free user's very first batch call gets a full audiobook of OpenAI+ElevenLabs spend. Also no job lock: calling the endpoint twice spawns two concurrent loops over the same chunks (cost ×2 + B4 corruption). | content-service/main.go:644-723 |
| B7 | **Stripe checkout has two hardcoded price line-items** (`price_1Rq20X...` and `price_1Rq1zU...`) — subscribers are signed up to both prices simultaneously. Verify intent; almost certainly one should be removed, and IDs belong in env/config. | auth-service/main.go:568-577 |
| B8 | **Stripe webhook handles only 2 events.** No `invoice.payment_failed`, no `customer.subscription.updated` (e.g. cancel-at-period-end → re-activated), no idempotency/event-id dedup. Failed renewals keep paid access until the subscription object is finally deleted. | auth-service/main.go:619-641 |

### A3. Audio pipeline correctness & cost (P1)

| # | Issue | Evidence |
|---|---|---|
| Q1 | **Every page's mood, ambient, Foley, and music prompts are derived from the book's FIRST page only.** All four analysis functions read `book.FilePath` and truncate to the first 200–1000 chars — page 300 of a thriller gets the ambience of page 1. Should use `chunk.Content`. | sound_effects.go:229-233, 573-579, 784-790; chat_prompt.go:52 |
| Q2 | **Foley effects are muted by a fade bug.** `afade=t=out:st=0:d=0.1` starts the fade-out at t=0, so each effect decays to silence 0.1 s in — likely why Foley "quality" keeps getting tweaked. | sound_effects.go:1034 |
| Q3 | **Background music is regenerated per page** — one GPT prompt + one ElevenLabs call per page with identical inputs (Q1) and the result is never cached (only Foley clips are). A 400-page book = 400 identical music generations. Cache by (book, mood). | sound_effects.go:951-962 |
| Q4 | **Multi-voice TTS is hard-enabled with no toggle** (`convertTextToAudio` → MultiVoice always; the documented `ENABLE_MULTIVOICE` flag doesn't exist in code), and each dialogue segment is a separate TTS API call + GPT-4o dialogue analysis per page. Cost multiplier of 3–10× vs single voice with no off switch. | tts_processing.go:563-566 |
| Q5 | **`amix` halves narration volume.** ffmpeg `amix` averages inputs by default; the 2-layer mix divides TTS by 2 (3-layer worse). Use `amix=...:weights=` or `normalize=0` with pre-scaled volumes. | sound_effects.go:462,476 |
| Q6 | **Chunks get stuck in "processing" forever.** In the batch loop, failures after TTS (book lookup, prompt, music, merge) `continue` without setting `tts_status=failed`; the TTSQueueJob worker likewise never recovers jobs stuck in "processing" after a crash/restart. | content-service/main.go:682-708; streamByChunkIds.go:92-137 |
| Q7 | **`processMergedChunks` error is silently ignored** — result assigned to `errs` but `if err != nil` checks the *other* variable (which is always nil there). | processChunksHandler.go:96-99 |
| Q8 | **`audio_url` returned by the pages API is off by one.** Built with 0-based `chunk.Index` into a route that treats the segment as 1-based and subtracts 1 — the URL points at the *previous* page's audio. (iOS builds its own URLs, masking the bug.) | content-service/main.go:415-416 vs audio_stream.go:46-48 |
| Q9 | **Whole-book effects path is a no-op + panic risk.** `processSoundEffectsAndMerge(book, hash, nil)` iterates an empty list (so `processBookConversion` never adds effects), and `book.ContentHash[:8]` panics when the hash is empty. | tts_processing.go:634; sound_effects.go:938,1005 |
| Q10 | **EPUB "text" extraction keeps raw HTML/CSS** (no tag stripping at chunk time — chunks contain markup, page counts inflate, and TTS prep has to scrub it per page); `cleanUTF8` is an unimplemented stub; PDF extraction loses layout/spacing (known rsc.io/pdf limitation). | document_chunker.go:268-294, 211-214 |
| Q11 | **Re-uploading a file to a book duplicates every chunk** (no delete of existing chunks before re-chunk), and `deleteBookHandler` orphans chunks, progress rows, and audio files on disk. | fileupload.go (no reset); content-service/main.go:335-353 |
| Q12 | **Async chunking path uses one INSERT per chunk** (`ChunkDocument`) — the path specifically chosen for *large* books is the slow one; `ChunkDocumentBatch` already exists and should be used in both paths. | document_chunker.go:56-82 vs 125-189 |

### A4. Architecture & hygiene (P2)

- **Zero tests** across ~9,000 lines Go + ~12,300 lines Swift; schema managed by `AutoMigrate` only (no versioned migrations, no rollback).
- **Duplicated, drifted auth code**: two `authMiddleware`s (one pins HMAC, one doesn't; one sets `user_id`, one doesn't — root cause of B1), two `extractToken`s, two `adminMiddleware`s, two FileTree implementations. Needs one shared internal package.
- **Monolith files**: auth-service is one 2,469-line main.go; content-service main.go 1,149 lines.
- **No DB hardening**: no connection pool settings, no query timeouts/contexts, no indexes beyond defaults, free-tier check makes a *synchronous HTTP call to auth-service per request* instead of reading the JWT claim or caching.
- **Gateway is a bare proxy**: no rate limiting (login/signup brute-forceable), no body-size cap, no proxy timeouts, no request IDs; emoji logs can't trace a request across services.
- **Dead/junk code**: `character_detection.go` entirely unused (multi-voice uses `analyzeDialogue` in tts_processing.go instead); `main.go.backup` files (two, untracked); `tts_elevenlab.go.pack`; `wait-for-posgres-old`; `story.txt`; content-service README is "# trigger deploy"; leftover "page 11" debug block in fileupload.go:163-172.
- **Docs drift is dangerous**: claude.md documents a security model ("users can only stream their own books") the code doesn't implement; MULTIVOICE/ENABLE docs describe a file and flag that don't exist; duplicated guides (IOS-CONNECT.md ×2, server.md, SOCIAL_LOGIN specs ×2 with a typo'd filename) will rot independently.
- **iOS**: JWT in `UserDefaults` (should be Keychain — flagged in 5 files); uploads via `URLSession.shared` foreground tasks (backgrounding kills a 50 MB upload mid-flight; needs a background session — or better, presigned PUTs per the architecture plan); ~96 uncommitted paths and **no git remote**.
- **Uncommitted backend work**: the ambient-soundscape/crossfade rewrite of sound_effects.go exists only on this machine.

---

## Part B — Fix Order

Ordered so that each phase closes the riskiest remaining gap, no phase depends on a later one, and the final phase hands off cleanly to `structureImprovmentPlan.md`. Items reference the inventory above.

### Phase 0 — Stop the bleeding (hours, no redesign)

Do these in one sitting; none requires architectural thought:

1. **Close the network** (S2, S11): remove public port mappings for 8082/8083/6379 from prod compose (keep them on the internal Docker network); route everything through nginx → gateway; add `/admin/auth/*` (or host-based rule) so auth-admin endpoints work through the gateway; set a Redis password regardless.
2. **Kill unauthenticated static serving** (S1): delete the `router.Static("/audio", ...)` line; serve covers through an authenticated handler or accept covers-only public exposure consciously (covers are arguably fine — audio is not).
3. **Require secrets** (S8): both services `log.Fatal` if `JWT_SECRET` is unset; delete the fallback constants.
4. **Stop logging secrets** (S9): mask the DSN password, delete the token `fmt.Println`.
5. **Fix B1** (one line): set `c.Set("user_id", ...)` in auth-service's middleware — account deletion starts working again.
6. **Disable `/restore-account`** (S5) until Phase 2 redesigns it (return 410 or feature-flag it off). Restoration via the signup-conflict path can wait; an account-takeover hole cannot.
7. **Commit + push**: backend `sound_effects.go` work, then create a private remote for the iOS repo and push everything (its first off-machine backup).

### Phase 1 — Authentication is actually authentication (1–2 days) ✅ DONE (June 12, 2026)

8. ~~**Verify Apple tokens properly** (S3)~~ ✅ — Hand-rolled stdlib JWKS verification: cached RSA keyset from `appleid.apple.com/auth/keys` (TTL 1h, refetch on kid miss), RS256-only keyfunc (rejects alg-confusion), then iss/aud/exp checks. Covered by `apple_verify_test.go` (valid / forged-sig / HMAC / wrong-aud / wrong-iss / expired).
9. ~~**Harden Google/Facebook** (S4)~~ ✅ — Google: `GOOGLE_CLIENT_ID` now mandatory at request time (audience always enforced) + iss check. Facebook: `debug_token` with the `<id>|<secret>` app token, requires `is_valid && app_id == FACEBOOK_APP_ID`. Both fail closed; startup logs a warning per unconfigured provider (chose warn-not-crash so a missing optional secret can't take down email login / local dev).
10. ~~**Rework account linking**~~ ✅ — `handleSocialLogin` takes `emailVerified`; auto-links to an existing email account only when the provider asserts the email is verified (Apple/Google `email_verified == "true"`; Facebook never), else returns `ErrLinkRequiresVerification` → HTTP 409, no JWT.
11. ~~**Fix B3**~~ ✅ — Grouped `.Or(db.Where(...).Where("restored_at IS NULL"))` so the filter binds to every branch; signup conflict response no longer leaks `original_username`/`history_id`/`deleted_at`.
12. ~~**Pin signing method** in content-service (S10)~~ ✅ — keyfunc now asserts `*jwt.SigningMethodHMAC`, matching auth-service. **Deferred:** the full shared `internal/authn` package extraction (separate Go modules, per-service Docker contexts) → Phase 5 packaging.

### Phase 2 — Ownership and account lifecycle (partially done — June 15, 2026)

Exploitable holes closed this pass (S6/S7/Q11/B2); admin/restore lifecycle (S5/S10) deferred per product call (iOS exposes only Apple sign-in; restore stays disabled; admin is behind `is_admin`).

13. ~~**Ownership middleware** (S6)~~ ✅ — `requireBookOwnership()` in `content-service/ownership.go` applied to every `:book_id` route; inline `verifyBookOwnership` on the body/form routes (upload, `/chunks/tts`, `/chunks/audio-by-id`). Returns 404 (not 403) on someone else's book.
14. ~~**Upload hardening** (S7)~~ ✅ — saves to `./uploads/{user_id}/{book_id}/original{ext}` (client filename never touches the path); allow-list extension via `validUploadExt`; `MAX_UPLOAD_BYTES` size cap (413). (Magic-byte sniffing skipped — unreliable for epub/mobi; path scheme + allow-list + cap is the real fix.)
15. **Admin deletion**: ~~column bugs (B2)~~ ✅ — `UserHistory` filtered by `original_user_id`, `UserBookHistory` by `user_history_id IN (subquery)`. **Deferred:** audit_log + wipe-nonce (S10).
16. **Redesign restore-account** (S5): **deferred** — stays disabled (410). Revisit when social restore is needed.
17. ~~**Cascade book deletion** (Q11)~~ ✅ — `deleteBookHandler` transactionally deletes chunks/progress/processed-groups/jobs + disk files; `resetBookContent` clears chunks/groups on re-upload.

### Phase 3 — Pipeline integrity ✅ DONE (June 15, 2026) — except Q4 (left as-is)

18. ~~**Per-job temp dirs** (B4)~~ ✅ — `mergeAudio` creates `os.MkdirTemp("", "narrafied-mix-*")`, dyn-seg/crossfade/final-bg/ambient-loop all derive from it with `defer RemoveAll`; concat list + music files are now unique; glob-based `cleanupTempFiles` deleted.
19. ~~**Mutex around `effectCache`** (B5)~~ ✅ — `effectCacheMu sync.RWMutex` (and a new `musicCacheMu` for Q3). `-race` test added.
20. ~~**Job locking + status state machine** (B6, Q6)~~ ✅ — atomic book claim (`status <> 'processing'`, 409 on duplicate); per-chunk atomic claim; every failure path sets `tts_status='failed'`; book lock released to `completed`/`pending`; `recoverStuckPipeline` startup sweep requeues stuck jobs/chunks/books.
21. ~~**Free-tier enforcement inside the loop** (B6)~~ ✅ — `FREE_TIER_PAGE_LIMIT` (default 1) re-checked via local DB count inside the batch loop; stops at cap.
22. ~~**Per-page analysis context** (Q1)~~ ✅ — `chunk.Content` threaded into segment/ambient/Foley/music-prompt functions (signatures take the excerpt now).
23. ~~**Foley fade** (Q2)~~ ✅ — fade-out starts at `clipDur-0.1` (ffprobe), skipped if clip <0.15 s.
24. ~~**Cache music** (Q3)~~ ✅ — `getOrGenerateBackgroundMusic` caches by prompt hash (also gives unique filenames for B4).
25. ~~**Mix levels** (Q5)~~ ✅ — explicit `amix … normalize=0:weights=1.0 0.3[ 0.15]`.
26. **Multi-voice toggle** (Q4): **skipped — left always-on per product decision.** Revisit with the account_type-in-claims work.
27. ~~**Correctness sweep**~~ ✅ — Q7 (right error var), Q8 (`audio_url` uses `Index+1`), Q9 (real pageIndexes + `shortHash` guard), Q12 (async uses `ChunkDocumentBatch`), Q10 (EPUB `stripHTML` + real `cleanUTF8`).
28. ~~**Tests**~~ ✅ — `pipeline_test.go`: `shortHash`, `stripHTML`, `cleanUTF8`, `effectCache` `-race`; plus existing `upload_path_test.go`.

### Phase 4 — Payments & lifecycle correctness ✅ DONE (June 15, 2026)

29. ~~**Double price line-item** (B7)~~ ✅ — checkout now bills a single subscription price from `STRIPE_PRICE_ID` (no hardcoded IDs); session carries `user_id` metadata. **Deploy/coord note:** point `STRIPE_PRICE_ID` at the intended price and reconcile $15 vs $24.99 across `PricingConfiguration.swift`, Stripe, and App Store Connect.
30. **Webhook coverage** (B8): ~~`invoice.payment_failed` (grace, no downgrade), `customer.subscription.updated` (status-driven tier), idempotency via `processed_stripe_events`~~ ✅. **Deferred:** daily Stripe→DB reconcile sweep + reconcile-on-restore (restore is disabled / S5; revisit with Phase 5 quotas).

### Phase 5 — Hygiene (backend slice done — June 15, 2026; rest deferred)

Backend-deployable slice shipped this pass; the monolith split, versioned migrations, iOS, and docs are separate efforts.

31. **Split the monoliths** — **deferred** (separate refactor). ~~Dead code removed~~ ✅: deleted `character_detection.go`, `*.backup`, `tts_elevenlab.go.pack`, `story.txt`, the fileupload page-11 debug block; rewrote `content-service/README.md`; `.gitignore` already covers `*.backup`.
32. **Versioned migrations** — **deferred** (golang-migrate; risky on the live external DB — do before the architecture migration).
33. ~~**Gateway hardening** (33)~~ ✅ — per-IP rate limit (`golang.org/x/time/rate`) on `/login`/`/signup`/`/auth/*`, `MAX_PROXY_BODY_BYTES` body cap, proxy + server timeouts, request-ID middleware (`X-Request-ID`) + JSON `slog` request logs.
34. ~~**DB client hygiene + account_type-in-claims** (34)~~ ✅ — connection pool sizing (`DB_MAX_OPEN`/`DB_MAX_IDLE`/30m lifetime) on both services; `account_type` now in JWT claims, content-service reads it from the claim with an HTTP fallback for pre-deploy tokens. (Statement-timeout/`context` on hot queries still TODO.)
35. **iOS** (Keychain token + background uploads) — **deferred** (separate Swift pass; 32 call sites mapped).
36. **Docs** regen/dedupe — **deferred** (separate pass).

Also done this pass (deferred from Phase 2): **S10** ✅ — admin `audit_logs` table + `auditMiddleware` on every `/admin` mutation; `/admin/system/wipe` now requires a short-lived single-use server nonce (`POST /admin/system/wipe/request`) instead of a hardcoded string. **S5** remains disabled (410) by decision.

### Phase 6 — Hand off to the architecture migration

With the above done, start [structureImprovmentPlan.md](structureImprovmentPlan.md) Phase 1 (object storage + CDN). The mapping of prerequisites:

| Architecture-plan phase | Blocked by (now fixed) |
|---|---|
| Storage swap (Spaces + signed URLs) | S1 (no more static dirs), S6 (ownership before URL minting), B4 (job temp dirs) |
| Queue + worker split | B4, B5, Q6 (state machine), 20 (job locking) |
| Presigned uploads | S7 (path/naming scheme), 14 (sniffed types), S6 |
| Quotas + notifications | 21 (in-loop enforcement seed), 34 (claims/caching), B8 (billing events) |

---

## Part C — Suggested working order summary (one line each)

1. ~~Ports/static/secrets/logging/user_id/restore-off/push repos~~ → **Phase 0, do today.**
2. ~~Social-login verification + signing-method pin~~ → **Phase 1 ✅** (shared-package extraction deferred to Phase 5).
3. ~~Ownership middleware + upload hardening + cascade delete + admin column fix~~ → **Phase 2 ✅** (restore redesign S5 + admin audit/wipe S10 deferred).
4. ~~Temp dirs, locks, per-page context, Foley fade, caching, tests~~ → **Phase 3 ✅** (multi-voice toggle Q4 left always-on per product decision).
5. ~~Stripe single price + webhook coverage + idempotency~~ → **Phase 4 ✅** (pricing reconciliation = deploy note; daily reconcile sweep deferred).
6. ~~Gateway hardening + DB pool + account_type-in-claims + S10 admin audit/wipe-nonce + dead-code~~ → **Phase 5 backend slice ✅** (monolith split, versioned migrations, iOS Keychain, docs still deferred).
7. Begin the storage/queue/presigned/quota migration → **Phase 6 / other doc.**
