# Narrafied Backend — Structure Improvement Plan

**Date:** June 11, 2026
**Status:** Proposal (no code changes yet)
**Goal:** Move from a server-does-everything design to the **control plane / data plane** split used by Spotify, Netflix, and Audible: the API servers make decisions (auth, quotas, orchestration), while object storage + CDN move the bytes, and a worker fleet does the heavy media processing.

---

## 1. Executive Summary

| Concern | Today | Target |
|---|---|---|
| Book upload | Multipart POST through nginx → gateway → content-service → local disk (`/opt/stream-audio-data/uploads`) | Client uploads **directly to DigitalOcean Spaces** via short-lived presigned URL; server never touches file bytes |
| Transcription | Fire-and-forget goroutines inside the API process; whole book processed in one shot | Durable **job queue** (Redis-backed) with a worker pool; books processed in **20-page batches**, user notified per batch, listening starts after batch 1 |
| Audio storage | Local disk on one droplet (`/opt/stream-audio-data/audio`) | All generated audio in **Spaces (S3-compatible object storage)**, served through the **Spaces CDN** with signed URLs |
| Audio delivery | Go service reads file from disk and proxies every byte to AVPlayer | CDN serves the bytes; API only issues time-limited signed URLs (entitlement check) |
| Business limits | Single hard-coded rule (free = 1 chunk) | Central **entitlement/metering service**: per-tier upload, transcription, and streaming quotas backed by Redis counters + Postgres usage ledger |

Why this matters: every byte of a 50 MB EPUB and every second of streamed audio currently flows through the Go process and a single droplet's disk. That couples CPU-bound media work with latency-sensitive API traffic, makes horizontal scaling impossible (local disk = sticky state), and makes uploads fragile (the nginx 413 incident, slow mobile uploads holding server connections open). The industry pattern for media-heavy apps is consistent: **clients talk to object storage and CDNs for bytes; application servers only mint permissions and track state.**

---

## 2. Current Architecture and Its Bottlenecks

```
iOS app ──HTTPS──► nginx (narrafied.com) ──► gateway :8080 ──► auth :8082
                                                  │                content :8083
                                                  ▼
                                   content-service does EVERYTHING:
                                   • receives file bytes (multipart)
                                   • extracts text (Calibre/PDF) inline
                                   • spawns goroutines for TTS/music/Foley
                                   • writes MP3/Opus to local disk
                                   • proxies every audio byte to AVPlayer
                                   • PostgreSQL + Redis (Redis barely used)
```

Specific problems found in the codebase and fix-log history:

1. **Uploads through the app server.** `uploadBookFileHandler` buffers the file, hashes it, extracts text, and chunks it inside the HTTP request. Large books hit nginx limits (the HTTP 413 incident), tie up connections for the duration of a mobile upload, and the file lands on one droplet's disk.
2. **In-process, all-or-nothing transcription.** `BatchTranscribeBookHandler` / `processBookConversion` run as goroutines in the API process. If the container restarts mid-book, work is lost (the `tts_queue_jobs` table exists but there is no durable worker/retry system). A 400-page book must largely finish before the user hears anything.
3. **Audio on local disk, proxied by Go.** `streamSinglePageAudioHandler` and friends `os.Open` files and copy them through the service. One slow reader occupies a server connection; disk is a single point of failure; you can never run two content-service replicas because state is on one machine.
4. **Quotas are an afterthought.** The only limit is "free account → 1 completed chunk," enforced inline. There's no usage ledger, no streaming or upload metering, nothing tied to the Stripe/IAP tier.
5. **Notifications assume a foreground app.** MQTT only reaches the device while the socket is open; there is no APNs path to tell a user "your first 20 pages are ready" after they backgrounded the app.

---

## 3. Target Architecture

```
                       ┌────────────────────────── DATA PLANE ──────────────────────────┐
                       │                                                                │
  iOS app ──(1) request presigned URL──► content-api (control plane)                   │
     │                                        │ checks quota, mints URL                │
     ├──(2) PUT book file ────────────────────┼──────────► DO Spaces  bucket: uploads/ │
     ├──(3) POST /uploads/:id/complete ──────►│                  │                     │
     │                                        │ enqueue parse    │                     │
     │                                   Redis queue (asynq)     │                     │
     │                                        │                  ▼                     │
     │                                   worker fleet ◄── pulls book from Spaces       │
     │                                   • extract text, chunk                         │
     │                                   • TTS + music + Foley per page                │
     │                                   • upload audio ─────► Spaces bucket: audio/   │
     │                                   • after each 20-page batch:                   │
     │                                        ├─ APNs push + MQTT event                │
     │                                        └─ auto-enqueue next batch               │
     │                                                           │                     │
     └──(4) GET signed CDN URL from api, then stream ◄── Spaces CDN edge ◄─────────────┘
                       │
                       └── control plane stores only METADATA + USAGE in PostgreSQL
```

**Control plane (Go services, stateless, horizontally scalable):** auth, book metadata, quota/entitlement checks, presigned-URL minting, job orchestration, progress, stats. No file bytes, no local disk.

**Data plane (managed infrastructure):** DigitalOcean Spaces (S3-compatible object storage — works with `boto3`, `aws-sdk-go-v2`, any S3 SDK) + its built-in CDN for delivery. Workers are the only compute that touches media bytes.

This is the same separation Netflix uses (orchestrator splits a title into chunks → queue → parallel stateless workers → object storage → CDN serves users) and Spotify uses (event-driven pipelines over queues; clients stream from CDN edges, never from application servers).

---

## 4. Direct-to-Storage Uploads (Presigned URLs)

### Flow

1. `POST /user/uploads/initiate` with `{filename, size_bytes, content_type, sha256}`.
   - Server validates the extension/MIME against the supported list, checks the user's **upload quota** and per-file size cap for their tier, and dedup-checks the SHA256 (if the hash already exists, skip upload entirely and reuse — today's dedup logic, moved earlier in the flow).
   - Server creates an `uploads` row (`status=pending`, expiry) and returns a **presigned PUT URL** scoped to key `uploads/{user_id}/{upload_id}/{filename}`, expiring in ~15 minutes, with `Content-Type` and `Content-Length` conditions baked into the signature.
2. iOS uploads the file **directly to Spaces** with `URLSession.uploadTask` (background-session capable — uploads survive app backgrounding, something the current multipart POST can't do).
3. `POST /user/uploads/{id}/complete` — client tells the server it finished.
   - **Important reality check:** DigitalOcean Spaces does **not** support S3 bucket event notifications/webhooks, so the design cannot rely on storage-triggered events the way AWS S3 + Lambda does. The completion signal must be the client's confirm call, backed by a **reconciliation sweeper** (cron worker that `HEAD`s pending uploads older than N minutes and either completes or expires them — catches clients that uploaded but crashed before confirming).
   - On complete: server verifies the object exists and the size/ETag matches, flips `status=uploaded`, and enqueues a `parse_book` job.

### Implementation notes

- **Multipart uploads for big files:** for files > ~100 MB (PDF scans, large EPUB collections), use S3 multipart upload — initiate server-side, presign each part URL, complete server-side. Spaces supports the multipart API.
- **CORS:** configure the Spaces bucket CORS policy for the app's origins (relevant if a web frontend lands later; native iOS doesn't enforce CORS).
- **Signature gotcha:** any header included when signing (e.g., `x-amz-acl`) must be sent verbatim by the client or the request fails with `SignatureDoesNotMatch`. Keep the signed header set minimal: `Content-Type` only. Objects stay **private**; never `public-read`.
- **Bucket layout:**
  ```
  narrafied-media/
    uploads/{user_id}/{upload_id}/original.{ext}     (source files; lifecycle-delete after 30d)
    audio/{book_id}/{page}/{content_hash}.mp3        (final mixed narration)
    audio/{book_id}/merged/{start}-{end}.mp3         (merged ranges, optional)
    covers/{book_id}/{hash}.jpg
    fx-cache/{prompt_hash}.ogg                       (shared Foley/music/ambient clip cache)
  ```
- The existing **content-hash dedup** becomes even cheaper: hash check happens at initiate-time, before any bytes move.

---

## 5. Progressive Transcription Pipeline (20-Page Batches)

### Queue and workers

- Adopt a **durable Redis-backed job queue** — [asynq](https://github.com/hibiken/asynq) is the strongest fit: Go-native, uses the Redis you already run, built-in retries with exponential backoff, scheduled jobs, priority queues, dead-letter queue, Prometheus metrics, and a web UI (asynqmon) for ops. (Alternative: River, if you'd rather keep job state transactionally in Postgres; asynq is recommended here because Redis is already deployed and throughput needs are modest.)
- Split the current content-service into two deployables built from the same codebase:
  - **content-api** — HTTP only. Enqueues jobs, never processes media. Can run 2+ replicas behind the gateway since it no longer owns local files.
  - **content-worker** — no HTTP. Runs the asynq consumer with a bounded concurrency pool (start: 2× vCPUs for FFmpeg-bound work). Scale by adding worker containers/droplets — this is exactly Netflix's chunk-encoding model: many idempotent stateless workers pulling from a queue.

### Job types

| Job | Work | Notes |
|---|---|---|
| `parse_book` | Download original from Spaces, extract text (Calibre/PDF/EPUB), chunk into pages, write `book_chunks`, then enqueue `transcribe_batch(book, pages 1–20)` | One per upload |
| `transcribe_batch` | For each of ≤20 pages: SSML/dialogue analysis → TTS (multi-voice) → music/Foley/ambient mix → upload final audio to Spaces → mark chunk `completed`. On batch completion: notify user + auto-enqueue the next batch | The core unit of progress |
| `transcribe_page` (optional refinement) | One page per job, with `transcribe_batch` as a lightweight coordinator | Gives page-level retries and parallelism across workers; recommended once worker count > 1 |
| `fetch_cover`, `reconcile_uploads`, `meter_flush` | Existing cover search; upload sweeper; usage-counter flush to Postgres | Move today's goroutines into the same queue for uniform retry/observability |

### The listen-while-we-transcribe loop (the UX you described)

1. Upload completes → `parse_book` → `transcribe_batch(1–20)` enqueued immediately.
2. Batch 1 finishes → **APNs push + MQTT event**: *"The first 20 pages of A Game of Thrones are ready — start listening!"* → app shows the book as playable with a "20 of 731 pages ready" progress indicator (the paginated `chunks/pages` endpoint already supports partial availability).
3. Batch 2 enqueues automatically when batch 1 completes (simplest), with one guard: **pause-ahead window** — don't transcribe more than N batches (e.g., 3 = 60 pages) beyond the user's current listening position for free-tier users. This caps API spend on books users abandon after page 10, which at OpenAI+ElevenLabs prices is your single largest variable cost. Playback-progress updates (already implemented) release the next batch.
4. Each subsequent batch completion fires a quieter notification ("Pages 21–40 ready") or just the MQTT event for in-app UI updates; final batch fires "Your audiobook is complete."

### Reliability rules (lifted from Netflix's worker design)

- **Idempotent jobs:** workers check Spaces for the audio object by content-hash key before generating; re-running a batch is harmless. (The content-hash caching already in the code makes this nearly free.)
- **Retries with DLQ:** transient OpenAI/ElevenLabs failures retry with backoff; after N attempts the job parks in the dead-letter queue and the chunk is marked `failed` so the UI can offer "retry page."
- **Graceful degradation stays:** music/Foley/ambient failures should never fail the batch — fall back to TTS-only audio (the code already leans this way; make it a hard rule in the worker).
- **Batch state machine on `books`:** `uploaded → parsing → partially_available (n_pages_ready) → completed / failed` replaces today's string statuses.

---

## 6. Media Storage & Delivery

### Storage: DigitalOcean Spaces

- S3-compatible (works with `aws-sdk-go-v2`, `boto3`, s3cmd), $5/mo for 250 GiB + 1 TiB transfer, built-in CDN at no extra cost.
- Replace every `os.Open`/`filepath` in streaming and cover handlers with object keys. Workers write with `PutObject`; nothing depends on droplet disk anymore (the `/opt/stream-audio-data` volumes become a temp scratch dir for FFmpeg intermediates only).

### Delivery: CDN with signed URLs — stop proxying audio through Go

Replace the byte-proxying stream handlers with an **entitlement-check + redirect** pattern:

1. `GET /user/books/:id/pages/:page/audio` (same route the app already calls).
2. Server checks: JWT valid → user owns book → **streaming quota OK** (see §7).
3. Server returns `302 Found` to a **presigned GET URL** on the Spaces CDN endpoint (TTL ~1–6 hours), or returns it in JSON for the player to use. AVPlayer follows redirects natively, so the iOS change is minimal-to-zero.
4. The CDN edge serves the bytes; repeat plays hit cache, not your droplet.

Notes:
- Presigned-GET TTL is the entitlement window — short enough that a leaked URL dies quickly, long enough to cover a listening session. This mirrors the token-protected CDN delivery (signed/tokenized URLs validated at the edge) that commercial audio platforms use.
- **HLS (later, optional):** AVPlayer's native protocol is HLS; segmenting pages into an HLS playlist would buy seamless page-to-page playback, adaptive bitrate, and better seeking for very long books. Recommended as a Phase-4 enhancement, not a prerequisite — per-page MP3 over CDN is a big enough win on its own.
- **Egress economics:** Spaces includes 1 TiB transfer, then ~$0.01/GiB. If streaming volume grows large, **Cloudflare R2 (zero egress fees)** is the standard cost-escape hatch — same S3 API, so the code written against Spaces ports unchanged. Decision can be deferred; the abstraction (one `MediaStore` interface) should not be.

---

## 7. Server as Business-Logic Control Plane: Quotas & Metering

This becomes the API tier's primary job. Industry best practice for tiered SaaS: **per-tenant, per-tier limits enforced from Redis counters, with an async-persisted usage ledger in Postgres, and limits wired to the billing tier.**

### Quota dimensions

| Quota | Free tier (example) | Premium | Enforced at |
|---|---|---|---|
| Uploads | 1 book, ≤ 25 MB | 20 books/mo, ≤ 250 MB | `POST /uploads/initiate` (hard cap) |
| Transcription | 20 pages total (exactly one free batch — a natural trial) | e.g. 2,000 pages/mo (soft cap) | before enqueueing each `transcribe_batch` |
| Streaming | e.g. 5 hours/mo | unlimited / fair-use 150 h/mo | before issuing each signed URL |
| Storage retention | originals deleted after 30 days | retained | lifecycle policy |

(Numbers are placeholders — set them from the cost analysis: TTS + music generation per page is the dominant cost, so **transcribed pages** is the quota that protects margin.)

### Mechanics

- **Redis counters** (`usage:{user_id}:{metric}:{period}`, fixed monthly window — fine for billing-style quotas) checked **synchronously/strictly** before the protected action: for cost protection, strict pre-checks are worth the ~1 ms latency.
- **Postgres `usage_events` ledger** (append-only: user, metric, amount, book, timestamp) flushed asynchronously from Redis — the audit trail for support disputes, the admin dashboard, and future overage billing.
- **Tier limits live in one table** (`plan_limits`), keyed by `account_type`, so changing the free tier doesn't mean redeploying — and Stripe/IAP webhooks already flip `account_type`, which automatically flips limits.
- Return `429` with a structured body (`{quota: "transcription_pages", used, limit, resets_at, upgrade_url}`) so the iOS app can render a contextual upgrade prompt — quota errors are the highest-converting paywall surface.
- Streaming metering: count signed-URL issuances × page duration (cheap, approximate) rather than measuring actual bytes at the CDN (Spaces doesn't expose per-object logs); approximate is fine when the goal is abuse prevention, not billing.

### Where enforcement lives

Keep it in **auth-service** (it already owns users, tiers, Stripe state) exposed as one internal endpoint: `POST /internal/quota/check-and-consume {user_id, metric, amount}` called by content-api and workers. One choke point, one implementation, every limit testable in one place.

---

## 8. Data Model Changes

```sql
-- new
uploads            (id, user_id, object_key, filename, size_bytes, sha256,
                    status: pending|uploaded|parsed|expired, created_at, confirmed_at)
usage_events       (id, user_id, metric, amount, book_id, created_at)        -- append-only ledger
plan_limits        (account_type, metric, monthly_limit, hard_cap bool)
transcription_batches (id, book_id, start_page, end_page,
                    status: queued|processing|ready|failed, completed_at)

-- changed
books        : file_path/audio_path → object keys; status → state machine (§5);
               + pages_ready int, total_pages int
book_chunks  : audio_path/final_audio_path → object keys; + batch_id
push_tokens  : (user_id, apns_token, platform, updated_at)  -- the User model already
               captures PushToken; promote it to a table for multi-device
-- removed/replaced
tts_queue_jobs → superseded by asynq (Redis) + transcription_batches
```

---

## 9. Notifications

- **APNs (new, required):** batch-ready and book-complete notifications must reach backgrounded/closed apps; MQTT cannot. Workers call APNs directly (token-based auth, `apns2` Go library) using the `PushToken` the app already sends for account-restoration fingerprinting.
- **MQTT (keep):** real-time in-app updates while foregrounded (progress ticks, cover-ready) — already built and working.
- Notification policy: batch 1 ready → loud push ("Start listening!"); intermediate batches → silent push/MQTT updating the progress bar; final batch → "Your audiobook is complete"; failure after retries → "We hit a snag on pages 41–60 — tap to retry."

---

## 10. Migration Plan (each phase independently shippable)

| Phase | Scope | Risk |
|---|---|---|
| **1. Storage swap** ✅ DONE (Jun 15 2026) | Cloudflare R2 instead of Spaces; `MediaStore` (`mediastore.go`), all media→R2 keys, streaming via 302 presigned + legacy fallback, existing files migrated via `rclone` + DB rewrite. | shipped |
| **2. Queue + worker split** ✅ DONE (Jun 15 2026) | asynq on the existing Redis; `RUN_MODE=api\|worker`; new `content-worker` container; progressive 20-page `transcribe:batch` (auto-enqueue next + MQTT `pages_ready`) + `chunks:merge` + `cover:fetch`; `transcription_batches` table; retired the `TTSQueueJob` poller. Verified: api enqueues, worker does all FFmpeg. | shipped |
| **3. Presigned uploads** ✅ BACKEND DONE (Jun 15 2026) | `PresignPut`; `/user/books/:id/upload/initiate` + `/complete` (book-centric, dedup by sha256); `book:parse` asynq job (chunks from R2 via `localizeMedia`); reconcile sweeper for stale `awaiting_upload`. Legacy multipart kept. **iOS pending build/ship:** `Services/PresignedUploadService.swift` (initiate → background-URLSession PUT → complete → poll) written, not yet released. Note: Content-Type isn't signature-enforced by the v2 presigner (fine — worker reads by key). | backend shipped; iOS app release pending |
| **4. Quotas + notifications** | `plan_limits`, `usage_events`, Redis counters, quota middleware, APNs sender, 20-page batch notifications + pause-ahead window | Medium — product decisions needed on limits |
| **5. Polish (optional)** | HLS packaging for seamless long-book playback; R2 evaluation if egress costs bite; asynqmon + Prometheus dashboards; autoscaling workers off queue depth | Low, incremental |

Phases 1–2 alone deliver the biggest wins: horizontally scalable API, durable processing that survives restarts, CDN-served audio, and droplet disk out of the critical path.

---

## 11. Cost & Scaling Notes

- **Spaces:** $5/mo base (250 GiB + 1 TiB egress) vs. growing droplet block storage; CDN included. A 400-page book ≈ 400 MP3s ≈ 0.5–1 GiB of audio.
- **Biggest variable cost is generation, not storage:** OpenAI TTS + GPT analysis + ElevenLabs per page. The **pause-ahead window** (§5) and **page quotas** (§7) are the cost controls — they matter more financially than any infrastructure choice in this document.
- **Worker scaling:** FFmpeg + API-bound work parallelizes linearly; queue depth is the autoscaling signal. The same 20-page batch that takes ~10 min serially drops to ~1 min with 10 page-level workers (Netflix's chunk-parallelism math, applied at our scale).
- **Egress hedge:** if monthly streaming egress approaches Spaces' included TiB, R2's zero-egress pricing typically cuts delivery cost dramatically for read-heavy media workloads; the S3-compatible abstraction makes this a config change, not a rewrite.

---

## 12. Sources

- [DigitalOcean — File permissions & presigned URLs](https://docs.digitalocean.com/products/spaces/how-to/set-file-permissions/)
- [DigitalOcean — Spaces S3 compatibility reference](https://docs.digitalocean.com/products/spaces/reference/s3-compatibility/)
- [Uploading directly to DigitalOcean Spaces from the browser (presigned PUT walkthrough)](https://carterbancroft.com/uploading-directly-to-digital-ocean-spaces-from-your-dang-browser/)
- [Presigned URL ACL/header signature pitfalls (DEV Community)](https://dev.to/richardj/upload-to-digitalocean-spaces-with-aws-s3-getsignedurl-with-correct-permissions-and-content-type-29og)
- [DigitalOcean community — Spaces has no native webhook/event notifications](https://www.digitalocean.com/community/questions/any-webhook-functionality-with-spaces)
- [FastPix — System design for an audio streaming app like Spotify](https://www.fastpix.io/blog/system-design-and-site-architecture-for-an-audio-streaming-app-like-spotify)
- [Tech Holding — Audio streaming & ingestion pipeline on AWS (case study)](https://techholding.co/casestudy/audio-streaming-ingestion-pipeline)
- [Dacast — HLS streaming protocol guide](https://www.dacast.com/blog/hls-streaming-protocol/)
- [Protecting HLS with CDN token authentication (Google Cloud community)](https://medium.com/google-cloud/protecting-hls-streaming-with-google-media-cdn-dual-tokean-authentication-using-hmac-tokens-9acf6c60c905)
- [Netflix TechBlog — Conductor: microservices orchestration for media pipelines](https://netflixtechblog.com/netflix-conductor-a-microservices-orchestrator-2e8d4771bf40)
- [How Netflix's video processing pipeline works (chunk → queue → parallel workers)](https://singhajit.com/netflix-video-processing-pipeline/)
- [Netflix TechBlog — Data reprocessing pipeline in asset management](https://netflixtechblog.com/data-reprocessing-pipeline-in-asset-management-platform-netflix-46fe225c35c9)
- [Spotify — stream processing & event delivery on Google Cloud](https://cloud.google.com/blog/products/gcp/spotifys-experiments-with-stream-processing-on-google-cloud-dataflow)
- [ByteByteGo — How Spotify built its data platform](https://blog.bytebytego.com/p/how-spotify-built-its-data-platform)
- [asynq — Redis-backed distributed task queue for Go](https://github.com/hibiken/asynq)
- [Building a job queue in Go with asynq and Redis (OneUptime)](https://oneuptime.com/blog/post/2026-01-07-go-asynq-job-queue-redis/view)
- [Task queues in Go: asynq vs Machinery vs Work](https://medium.com/@geisonfgfg/task-queues-in-go-asynq-vs-machinery-vs-work-powering-background-jobs-in-high-throughput-systems-45066a207aa7)
- [Cloudflare R2 vs DigitalOcean Spaces comparison (Taloflow)](https://www.taloflow.ai/guides/comparisons/cloudflarer2-vs-digitaloceanspaces-object-storage)
- [S3 vs R2 vs Spaces vs Wasabi (Nubbo)](https://nubbo.app/blog/s3-vs-r2-vs-spaces-vs-wasabi/)
- [Per-tenant rate limiting & quota monitoring for SaaS (OneUptime)](https://oneuptime.com/blog/post/2026-02-06-saas-api-rate-limiting-quota-opentelemetry/view)
- [API cost protection: rate limits, quotas, spending caps (Zuplo)](https://zuplo.com/learning-center/api-cost-protection-rate-limits-quotas-spending-caps)
- [Scalable rate limiting & quota management architecture](https://medium.com/@hafeez.fijur/scalable-api-rate-limiting-system-quota-management-system-f936e827ae53)
- [Aligning rate-limit algorithms with business tiers](https://medium.com/@digvijay17july/how-to-align-rate-limiting-algorithms-with-business-tiers-usage-quotas-and-ai-gateway-policies-98e0a5d0d2b3)
