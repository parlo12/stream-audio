# On-Device Testing Session — HLS-Primary, Look-Ahead & Upload Wiring

**Date:** 2026-06-15 (continued into early 2026-06-16 UTC)
**Scope:** Wire the migration Phase 3/5 features into the iOS app, test the whole
flow on a real iPhone, and fix everything that surfaced. Ended with **HLS as the
primary playback path** via look-ahead packaging, all merged and deployed.

> This doc is the resume point. Read the "Resume Checklist" and "Open Items"
> sections first if you're picking this back up.

---

## TL;DR — what changed this session

- The iOS app's new features (presigned upload, HLS playback) were **written but
  never wired into the UI**. We wired them, tested on device, and fixed a chain
  of real bugs.
- **HLS is now the primary playback path.** Pages are transcribed + HLS-packaged
  a few pages *ahead* of the listener, so playback streams HLS (not the MP3
  fallback). Verified live across ~14 pages on a real iPhone.
- Everything is **committed, pushed, and deployed to prod.** Both repos are on a
  single clean `main`.

---

## Production state (as of session end)

| Thing | State |
|---|---|
| Backend `main` | `5240b82` — deployed to prod, `content=200 auth=200` |
| iOS `main` | `9222a9e` — all session work merged; Debug build on the test iPhone |
| Host | `stream-app` (DigitalOcean), deploy dir `/opt/stream-audio/stream-audio` |
| Compose | `docker-compose.prod.yml`, services: content-service (api), content-worker, auth, postgres(local, unused in prod), redis, asynqmon, prometheus, grafana |
| DB | External DO managed Postgres (`defaultdb`, user `doadmin`, host `private-streaming-db-...:25060`, sslmode=require). The local `postgres` container is NOT the prod DB. |
| Storage | Cloudflare R2 bucket `narrafied-media` (rclone remote `r2:` on the host) |
| Grafana | `grafana/grafana:11.2.0` (pinned — `latest` crash-loops on datasource provisioning) at `/admin/grafana/` |

---

## Backend commits this session (newest → oldest)

| Commit | What | Why |
|---|---|---|
| `5240b82` | **HLS-primary via look-ahead** | New `transcribe:lookahead` asynq task; transcribe + HLS-package N pages ahead of the listener so HLS is ready before they arrive |
| `148f83b` | **HEAD route for `hls.m3u8`** | The app probes `HEAD .../hls.m3u8`; the route was GET-only and Gin doesn't auto-serve HEAD, so the probe always 404'd → app never used HLS |
| `4ae5c7d` | **Cover select: non-blocking + R2** | Failed cover download returned a hard 500 (blocked upload flow) and still wrote to local disk; now soft 422 + uploads to R2 via `storeCover` |
| `aad2cbe` | **HLS enqueue from the play path** | The app's Play uses `/chunks/tts` → `processSoundEffectsAndMerge`, which never enqueued HLS; only `transcribePage` did. Now both do. |
| `6d63e5c` | **`no_text_extracted` status** | Scanned/image-only PDFs parse to zero text; distinct status → tailored "scanned PDF" client message (and `SkipRetry`) |

(Earlier in the broader effort: Phase 3 presigned uploads `8deef86`, Phase 4
quotas `a358516`, Phase 5A/B/C observability + HLS `e3f2c46`/`0cdf298`/`69265f2`.)

## iOS commits this session (all now on `main`)

| Commit | What |
|---|---|
| `9222a9e` | Auto-play actually plays — poll confirms readiness then play **directly** (don't route through `playCurrentPage()`'s stale in-memory status guard) |
| `b095db1` | Cover-select shows the server's friendly message (`APIError: LocalizedError` + `serverMessage` case) |
| `a0eeb9a` | Auto-play when audio is ready: poll the **audio endpoint** (not the premature `status`), keep "Generating audio…" up, auto-play on ready |
| `2869365` | Scanned-PDF-specific upload error message |
| `1b0c2fd` | Wire presigned upload + HLS playback into the UI (the headline wiring) |
| `43703eb` | HLS playback with per-page MP3 fallback (`playPage`) |
| `819d00e` | PresignedUploadService (initiate → background PUT → complete → poll) |

---

## How HLS-primary + look-ahead works now (architecture)

**Per-page pipeline (identical for play + look-ahead):**
`convertTextToAudio` (TTS) → `processSoundEffectsAndMerge` (background music +
Foley → final audio in R2) → enqueues `hls:package` → ffmpeg segments the final
audio into HLS (`audio/{book}/{page}/hls/`), stores the playlist key on the chunk.

**Look-ahead (the new bit — `content-service/queue.go`):**
- `TaskLookAhead{BookID, StartIndex, Count, UserID, AccountType}` / task type
  `transcribe:lookahead`.
- `handleLookAhead` → for each page in the window: if not transcribed,
  `lookAheadTranscribeChunk` (same pipeline, synchronous, idempotent atomic
  claim); if transcribed but no HLS, enqueue `hls:package`.
- Triggered from **two places** so it follows the listener:
  1. `ProcessChunksTTSHandler` (`/chunks/tts`) after the requested page.
  2. `UpdatePlaybackProgressHandler` (`playback_progress.go`) as progress advances.
- Window size: `LOOKAHEAD_PAGES` env (default **3**). Bounds cost/worker load.

**Serving (`content-service/hls.go`):**
- `GET /user/books/:id/pages/:page/hls.m3u8` → `serveHLSHandler`: fetch playlist
  from R2, rewrite each segment line to a short-TTL **presigned R2 URL**.
- `HEAD ...` → `headHLSHandler`: cheap existence check (returns 200 if `HLSPath`
  set, else 404). **Required** — without it the app's probe 404s and HLS is unused.

**iOS (`AudioPlayerService.playPage` / `BookPlaybackViewModel`):**
- `playPage(bookID:page:)` HEADs the HLS playlist; 200 → play HLS, else per-page MP3.
- Play flow: tap → if not completed, `requestTTSIfNeeded` POSTs `/chunks/tts` →
  keep "Generating audio…" indicator up → `pollForTranscriptionCompletion` polls
  the **audio endpoint** until 200/302/206 → **auto-play directly** via
  `playAudioFromNewRoute` (only if still on that page).

**Net effect:** first page tapped in a fresh spot is MP3 (its audio must exist
before HLS can be cut), but every auto-advanced/resumed page after is **HLS**,
because look-ahead packaged it while you listened to the previous page.

---

## Bugs found & fixed on device (chronological — the story)

1. Neither feature was wired into the UI (`playPage`/`PresignedUploadService` had
   zero callers). → wired both.
2. Scanned PDF uploaded fine but `chunking_failed` (no text layer). → distinct
   status + friendly message. (Not a bug — expected; just bad messaging.)
3. Free `uploads` quota was a placeholder `1/month` → 429 blocked re-upload. →
   lifted free-tier caps for testing (see Open Items).
4. Play transcribed but **never packaged HLS** — Play uses `/chunks/tts`, only
   `transcribePage` enqueued HLS. → enqueue from `processSoundEffectsAndMerge`.
5. Auto-play "did nothing" — poll trusted premature `status==completed` then
   played too early (404). → poll the audio endpoint instead.
6. Auto-play *still* did nothing — poll confirmed readiness but
   `playCurrentPage()` re-checked a **stale in-memory status** and bailed. → play
   directly.
7. HLS packaged but app never used it — `HEAD hls.m3u8` 404'd because the route
   was GET-only. → add HEAD route.
8. Cover-select 500-blocked the flow + wrote to local disk. → soft 422 + R2.

---

## Resume Checklist (how to pick this back up)

**Repos**
- Backend: `~/Desktop/RMH-Real-Estate/stream-audio` (branch `main`)
- iOS: `~/Desktop/RMH-Real-Estate/AudioBook` (branch `main`, single branch now)

**Build + install iOS to the test iPhone** (ROLF's iPhone 14 Pro Max):
```bash
cd ~/Desktop/RMH-Real-Estate/AudioBook
DEV_BUILD=00008120-001C15AC3AEBC01E    # xcodebuild device id
DEV_RUN=CAC9F2E2-578E-5002-8CAE-54479511875C   # devicectl CoreDevice id
xcodebuild build -project AudioBook.xcodeproj -scheme AudioBook -configuration Debug \
  -destination "id=$DEV_BUILD" -derivedDataPath /tmp/audiobook-dd -allowProvisioningUpdates
xcrun devicectl device install app --device "$DEV_RUN" /tmp/audiobook-dd/Build/Products/Debug-iphoneos/AudioBook.app
xcrun devicectl device process launch --device "$DEV_RUN" com.rmhrealestate.AudioBook
```
- Bundle id `com.rmhrealestate.AudioBook`, team `G9DTNH7ZNA`, automatic signing.
- App points at `https://narrafied.com` (prod) via `APIConstants.baseURL`.

**Deploy backend:**
```bash
ssh stream-app
cd /opt/stream-audio/stream-audio && git pull origin main
docker compose -f docker-compose.prod.yml up -d --build content-service content-worker
# Tip: build ONE service at a time; pulling heavy images + a Go build at once
# once spiked load to ~71 on the 2-core droplet (site stayed up; SSH was slow ~2min).
```

**Watch traffic during testing** (line-buffered, prints method/path/status):
```bash
ssh stream-app "stdbuf -oL tail -F /var/log/nginx/access.log | stdbuf -oL grep -aE \
  'upload/initiate|upload/complete|chunks/tts|hls.m3u8|pages/[0-9]+/audio' | \
  stdbuf -oL awk '{print \$1,\$4,\$6,\$7,\$9}'"
```

**Query prod DB** (psql lives in the local postgres container, point it at the remote):
```bash
ssh stream-app 'cd /opt/stream-audio/stream-audio && \
  PW=$(docker compose -f docker-compose.prod.yml exec -T content-service env | sed -n "s/^DB_PASSWORD=//p") && \
  docker compose -f docker-compose.prod.yml exec -T -e PGPASSWORD="$PW" postgres \
  psql "sslmode=require host=private-streaming-db-do-user-15814952-0.k.db.ondigitalocean.com port=25060 user=doadmin dbname=defaultdb" \
  -tA -c "SELECT ..."'
```

**Test fixtures still live:**
- `testuser` (user_id **6**), book **64** "The Demon Girl" (.txt, 406 pages,
  ~16 pages transcribed + HLS). Kept on purpose for more testing.
- Look-ahead config: `LOOKAHEAD_PAGES` (default 3), `PAUSE_AHEAD_PAGES` (default 60).

---

## Open Items / TODO before launch

1. **Restore real free-tier quota numbers.** They are currently maxed for testing
   (`free`: uploads/transcribe `100000`, stream `1000000`, all soft). Paid is
   *lower* than the testing free tier right now (uploads 20, transcribe 1000) —
   revisit both. `checkAndConsume` reads `plan_limits` live (no restart needed).
   Monthly usage counters: Redis `usage:{userID}:{metric}:{YYYY-MM}`.
2. **Rotate the exposed R2 keys.** Account `55815367385798d40080826842699e06`,
   access key `a51954...` was pasted in chat earlier — rotate + delete the
   redundant second token.
3. **HLS `.ts` orphan cleanup on book delete.** Cascade book-delete removes the
   tracked final audio + playlist key but not the HLS `.ts` segments (only the
   playlist key is DB-tracked). Fix: delete by `audio/{book}/` prefix on delete.
   (Cleaned manually for test books this session.)
4. **First-page-of-a-session is still MP3.** Inherent (audio must exist before HLS
   can be cut). If you want even that to be HLS, would need pre-generation before
   first play. Not recommended — current tradeoff is right.
5. **APNs push** — separate future pass (needs Apple `.p8` + iOS push registration).
6. **Set `GRAFANA_ADMIN_PASSWORD`** in the server `.env` (currently default admin/admin
   behind its own login at `/admin/grafana/`).

---

## Verified live on-device this session

Presigned upload (initiate 200 → complete 202) · book parse (406 chunks) ·
auto-transcribe · auto-play (manual tap + auto-advance) · **HLS streaming across
~14 pages** (`HEAD hls 200` → `GET hls 200` per page) · look-ahead packaging
pages ahead of the listener · scanned-PDF + cover-select friendly errors.
