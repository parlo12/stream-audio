package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hibiken/asynq"
)

// maybeResumeTranscription re-starts a book that was paused ahead of the
// listener once they've advanced enough that the next pending batch is back
// inside the pause-ahead window. Called from the playback-progress handler.
func maybeResumeTranscription(accountType string, bookID uint, chunkIndex int) {
	var b Book
	if err := db.First(&b, bookID).Error; err != nil || b.Status != "paused_ahead" {
		return
	}
	var res struct{ Min *int }
	db.Model(&BookChunk{}).Select("MIN(\"index\") as min").
		Where("book_id = ? AND tts_status <> ?", bookID, "completed").Scan(&res)
	if res.Min == nil {
		return // nothing left to transcribe
	}
	start := *res.Min
	if start > chunkIndex+pauseAheadPages() {
		return // listener still hasn't caught up to the window
	}
	db.Model(&Book{}).Where("id = ?", bookID).Update("status", "transcribing")
	if err := enqueueTranscribeBatch(bookID, start, start+batchSizePages-1, b.UserID, accountType); err != nil {
		log.Printf("⚠️ resume: enqueue batch for book %d failed: %v", bookID, err)
	} else {
		log.Printf("▶️ resumed transcription for book %d at page %d", bookID, start)
	}
}

// listenerChunkIndex returns the user's current listening page index for a book
// (0 if they haven't started) — used by the pause-ahead gate.
func listenerChunkIndex(userID, bookID uint) int {
	var pp PlaybackProgress
	if err := db.Where("user_id = ? AND book_id = ?", userID, bookID).First(&pp).Error; err != nil {
		return 0
	}
	return pp.ChunkIndex
}

// ---- task types & payloads ----

const (
	TypeTranscribeBatch = "transcribe:batch"
	TypeMergeChunks     = "chunks:merge"
	TypeFetchCover      = "cover:fetch"
	TypeParseBook       = "book:parse"
	TypeHLSPackage      = "hls:package"
	TypeLookAhead       = "transcribe:lookahead"
)

const batchSizePages = 20

type TaskTranscribeBatch struct {
	BookID      uint   `json:"book_id"`
	StartPage   int    `json:"start_page"` // chunk index (0-based)
	EndPage     int    `json:"end_page"`
	UserID      uint   `json:"user_id"`
	AccountType string `json:"account_type"`
}

type TaskMergeChunks struct {
	BookID uint `json:"book_id"`
}

type TaskFetchCover struct {
	BookID uint   `json:"book_id"`
	Title  string `json:"title"`
	Author string `json:"author"`
}

type TaskParseBook struct {
	BookID uint `json:"book_id"`
}

type TaskHLSPackage struct {
	BookID    uint `json:"book_id"`
	PageIndex int  `json:"page_index"`
}

// TaskLookAhead asks the worker to transcribe + HLS-package a small window of
// pages just ahead of the listener, so HLS is the primary playback path (ready
// before the user arrives) rather than always falling back to per-page MP3.
type TaskLookAhead struct {
	BookID      uint   `json:"book_id"`
	StartIndex  int    `json:"start_index"` // first chunk index (0-based) to ensure ready
	Count       int    `json:"count"`       // how many pages ahead to cover
	UserID      uint   `json:"user_id"`
	AccountType string `json:"account_type"`
}

// TranscriptionBatch tracks progress of one 20-page transcription batch.
type TranscriptionBatch struct {
	ID          uint   `gorm:"primaryKey"`
	BookID      uint   `gorm:"index"`
	StartPage   int
	EndPage     int
	Status      string `gorm:"default:'queued'"` // queued|processing|ready|failed
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// qClient is the process-wide asynq enqueuer (set in all run modes).
var qClient *asynq.Client

func redisConnOpt() (asynq.RedisConnOpt, error) {
	return asynq.ParseRedisURI(getEnv("REDIS_URL", "redis://redis:6379"))
}

// initQueueClient wires the enqueuer to Redis.
func initQueueClient() error {
	opt, err := redisConnOpt()
	if err != nil {
		return err
	}
	qClient = asynq.NewClient(opt)
	return nil
}

// startAsyncWorker runs the asynq consumer (blocks). Used in worker/both modes.
func startAsyncWorker() error {
	opt, err := redisConnOpt()
	if err != nil {
		return err
	}
	concurrency := envInt("WORKER_CONCURRENCY", 2*runtime.NumCPU())
	srv := asynq.NewServer(opt, asynq.Config{Concurrency: concurrency})

	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeTranscribeBatch, handleTranscribeBatch)
	mux.HandleFunc(TypeMergeChunks, handleMergeChunks)
	mux.HandleFunc(TypeFetchCover, handleFetchCover)
	mux.HandleFunc(TypeParseBook, handleParseBook)
	mux.HandleFunc(TypeHLSPackage, handleHLSPackage)
	mux.HandleFunc(TypeLookAhead, handleLookAhead)

	// Reconciliation sweeper: catch uploads that were initiated but whose
	// client died before confirming (R2 has no bucket-event webhooks).
	go reconcileUploadsLoop()

	log.Printf("🛠️  asynq worker starting (concurrency=%d)", concurrency)
	return srv.Run(mux)
}

func enqueueParseBook(bookID uint) error {
	b, _ := json.Marshal(TaskParseBook{BookID: bookID})
	_, err := qClient.Enqueue(asynq.NewTask(TypeParseBook, b),
		asynq.MaxRetry(3), asynq.Timeout(15*time.Minute), asynq.Queue("default"))
	return err
}

// ---- enqueue helpers ----

func enqueueTranscribeBatch(bookID uint, start, end int, userID uint, accountType string) error {
	b, _ := json.Marshal(TaskTranscribeBatch{BookID: bookID, StartPage: start, EndPage: end, UserID: userID, AccountType: accountType})
	_, err := qClient.Enqueue(asynq.NewTask(TypeTranscribeBatch, b),
		asynq.MaxRetry(5), asynq.Timeout(30*time.Minute), asynq.Queue("default"))
	return err
}

func enqueueMergeChunks(bookID uint) error {
	b, _ := json.Marshal(TaskMergeChunks{BookID: bookID})
	_, err := qClient.Enqueue(asynq.NewTask(TypeMergeChunks, b),
		asynq.MaxRetry(5), asynq.Timeout(30*time.Minute), asynq.Queue("default"))
	return err
}

func enqueueHLSPackage(bookID uint, pageIndex int) error {
	if qClient == nil {
		return fmt.Errorf("queue client not initialized")
	}
	b, _ := json.Marshal(TaskHLSPackage{BookID: bookID, PageIndex: pageIndex})
	_, err := qClient.Enqueue(asynq.NewTask(TypeHLSPackage, b),
		asynq.MaxRetry(3), asynq.Timeout(10*time.Minute), asynq.Queue("default"))
	return err
}

// enqueueLookAhead schedules transcription + HLS packaging for `count` pages
// starting at startIndex. Cheap to over-call: duplicate windows just find pages
// already done (idempotent claim) and no-op.
func enqueueLookAhead(bookID uint, startIndex, count int, userID uint, accountType string) error {
	if qClient == nil || count <= 0 {
		return nil
	}
	if startIndex < 0 {
		startIndex = 0
	}
	b, _ := json.Marshal(TaskLookAhead{BookID: bookID, StartIndex: startIndex, Count: count, UserID: userID, AccountType: accountType})
	_, err := qClient.Enqueue(asynq.NewTask(TypeLookAhead, b),
		asynq.MaxRetry(2), asynq.Timeout(30*time.Minute), asynq.Queue("default"))
	return err
}

func enqueueFetchCover(bookID uint, title, author string) error {
	b, _ := json.Marshal(TaskFetchCover{BookID: bookID, Title: title, Author: author})
	_, err := qClient.Enqueue(asynq.NewTask(TypeFetchCover, b),
		asynq.MaxRetry(3), asynq.Timeout(2*time.Minute), asynq.Queue("default"))
	return err
}

// ---- handlers ----

// transcribePage runs the full TTS→music→mix→R2 pipeline for one chunk and is
// idempotent (atomic claim skips already-processing/completed chunks).
func transcribePage(book Book, chunk BookChunk, userID uint, accountType string) error {
	claim := db.Model(&BookChunk{}).
		Where("id = ? AND tts_status NOT IN ?", chunk.ID, []string{"processing", "completed"}).
		Update("tts_status", "processing")
	if claim.RowsAffected == 0 {
		return nil // already done or in-flight elsewhere (don't double-consume quota)
	}

	// Consume one transcription page from the monthly budget (only on a fresh
	// claim, so retries never double-count). On deny, release the claim and
	// signal the batch to stop.
	if d := checkAndConsume(userID, accountType, "transcribe_pages", 1, book.ID); !d.Allowed {
		db.Model(&BookChunk{}).Where("id = ?", chunk.ID).Update("tts_status", "pending")
		return errQuotaExceeded
	}

	fail := func() { db.Model(&BookChunk{}).Where("id = ?", chunk.ID).Update("tts_status", "failed") }

	audioPath, err := convertTextToAudioForChunk(chunk)
	if err != nil {
		fail()
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(chunk.Content)))
	// Audit H2: score-palette cue (one musical identity per book), with the
	// legacy per-page prompt path as fallback inside.
	bgMusic, err := backgroundMusicForPage(book, chunk.Content)
	if err != nil {
		fail()
		return err
	}
	mergedAudio, err := mergeAudio(audioPath, bgMusic, book, chunk.Index, chunk.Content, hash)
	if err != nil {
		fail()
		return err
	}
	// Foley on the batch path too (decision after audit §4 gap): same
	// treatment as on-demand pages. Library-cached clips make this ~one
	// gpt-4o-mini call per fiction page; nonfiction skips inside.
	mergedAudio = applyFoleyOverlay(mergedAudio, audioPath, book, chunk.Index, chunk.Content)
	key, err := uploadArtifact(context.Background(), mergedAudio,
		audioPageKey(book.ID, chunk.Index, hash, filepath.Ext(mergedAudio)))
	if err != nil {
		fail()
		return err
	}
	db.Model(&BookChunk{}).Where("id = ?", chunk.ID).Updates(map[string]interface{}{
		"audio_path":       key,
		"final_audio_path": key,
		"tts_status":       "completed",
		// New final audio invalidates any previously packaged HLS — the
		// packager's already-packaged guard would otherwise keep serving the
		// old playlist after a re-render.
		"hls_path": "",
	})
	// Follow-on: package this page as HLS (non-blocking — doesn't gate playback).
	if err := enqueueHLSPackage(book.ID, chunk.Index); err != nil {
		log.Printf("⚠️ failed to enqueue HLS for book %d page %d: %v", book.ID, chunk.Index, err)
	}
	return nil
}

func upsertBatch(bookID uint, start, end int, status string) {
	var b TranscriptionBatch
	if err := db.Where("book_id = ? AND start_page = ? AND end_page = ?", bookID, start, end).First(&b).Error; err != nil {
		b = TranscriptionBatch{BookID: bookID, StartPage: start, EndPage: end, Status: status}
		db.Create(&b)
		return
	}
	updates := map[string]interface{}{"status": status}
	if status == "ready" || status == "failed" {
		now := time.Now()
		updates["completed_at"] = &now
	}
	db.Model(&TranscriptionBatch{}).Where("id = ?", b.ID).Updates(updates)
}

func handleTranscribeBatch(ctx context.Context, t *asynq.Task) error {
	var p TaskTranscribeBatch
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("bad payload: %v: %w", err, asynq.SkipRetry)
	}
	var book Book
	if err := db.First(&book, p.BookID).Error; err != nil {
		return fmt.Errorf("book %d not found: %w", p.BookID, err) // retryable
	}
	upsertBatch(p.BookID, p.StartPage, p.EndPage, "processing")

	var chunks []BookChunk
	db.Where("book_id = ? AND \"index\" BETWEEN ? AND ? AND tts_status <> ?", p.BookID, p.StartPage, p.EndPage, "completed").
		Order("\"index\" ASC").Find(&chunks)

	capped := false
	for _, ch := range chunks {
		// transcribePage consumes the per-page quota on a fresh claim; a quota
		// denial stops the batch.
		if err := transcribePage(book, ch, p.UserID, p.AccountType); err != nil {
			if errors.Is(err, errQuotaExceeded) {
				log.Printf("🛑 transcription quota reached for user %d; stopping book %d", p.UserID, p.BookID)
				capped = true
				break
			}
			log.Printf("⚠️ page %d (book %d) failed: %v", ch.Index, p.BookID, err)
		}
	}
	upsertBatch(p.BookID, p.StartPage, p.EndPage, "ready")

	// Notify (MQTT): how many pages are now playable.
	var ready int64
	db.Model(&BookChunk{}).Where("book_id = ? AND tts_status = ?", p.BookID, "completed").Count(&ready)
	publishPagesReady(book, int(ready))

	// Push notification (best-effort, non-blocking). One message per batch, no
	// double-fire: fully done → "complete"; first batch → "ready to play";
	// otherwise → "more pages ready".
	var notDone int64
	db.Model(&BookChunk{}).Where("book_id = ? AND tts_status <> ?", p.BookID, "completed").Count(&notDone)
	switch {
	case notDone == 0:
		notifyBookCompleted(book)
	case p.StartPage == 0:
		notifyAudiobookReady(book)
	default:
		notifyBatchReady(book, int(ready))
	}

	// Auto-enqueue the next batch if there's more to do (and not quota-capped).
	var pendingBeyond int64
	db.Model(&BookChunk{}).Where("book_id = ? AND \"index\" > ? AND tts_status <> ?", p.BookID, p.EndPage, "completed").Count(&pendingBeyond)
	if !capped && pendingBeyond > 0 {
		// Pause-ahead: for free users, don't transcribe more than
		// PAUSE_AHEAD_PAGES beyond where they're currently listening. Resumed by
		// UpdatePlaybackProgressHandler when the listener advances.
		nextStart := p.EndPage + 1
		// Lazy transcription for EVERY tier (not just free): never transcribe
		// more than PAUSE_AHEAD_PAGES beyond the listener — this is what makes
		// large books (thousands of chunks) tractable.
		if nextStart > listenerChunkIndex(p.UserID, p.BookID)+pauseAheadPages() {
			db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "paused_ahead")
			log.Printf("⏸️ book %d paused ahead (next page %d, listener+window)", p.BookID, nextStart)
			return nil
		}
		if err := enqueueTranscribeBatch(p.BookID, nextStart, p.EndPage+batchSizePages, p.UserID, p.AccountType); err != nil {
			log.Printf("⚠️ failed to enqueue next batch for book %d: %v", p.BookID, err)
		}
		return nil
	}

	// No more batches: release the book lock.
	var remaining int64
	db.Model(&BookChunk{}).Where("book_id = ? AND tts_status <> ?", p.BookID, "completed").Count(&remaining)
	if remaining == 0 {
		db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "completed")
		log.Printf("✅ Book %d fully transcribed", p.BookID)
	} else {
		db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "pending")
	}
	return nil
}

func handleMergeChunks(ctx context.Context, t *asynq.Task) error {
	var p TaskMergeChunks
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("bad payload: %v: %w", err, asynq.SkipRetry)
	}
	return processMergedChunks(p.BookID)
}

func handleFetchCover(ctx context.Context, t *asynq.Task) error {
	var p TaskFetchCover
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("bad payload: %v: %w", err, asynq.SkipRetry)
	}
	bookIDStr := fmt.Sprintf("%d", p.BookID)
	coverKeyOrPath, publicURL, err := fetchAndSaveBookCover(p.Title, p.Author, bookIDStr)
	if err != nil {
		return err // retryable
	}
	if err := db.Model(&Book{}).Where("id = ?", p.BookID).Updates(map[string]interface{}{
		"cover_path": coverKeyOrPath,
		"cover_url":  publicURL,
	}).Error; err != nil {
		return err
	}
	var book Book
	if err := db.First(&book, p.BookID).Error; err == nil {
		payload, _ := json.Marshal(map[string]interface{}{"book_id": book.ID, "cover_url": publicURL, "timestamp": time.Now().UTC().Format(time.RFC3339)})
		PublishEvent(fmt.Sprintf("users/%d/cover_uploaded", book.UserID), payload)
		notifyCoverReady(book)
	}
	return nil
}

// handleParseBook downloads the uploaded source from R2 (via ChunkDocumentBatch
// → ExtractTextByType, which localizes the key), chunks it, and marks the book
// ready for transcription.
func handleParseBook(ctx context.Context, t *asynq.Task) error {
	var p TaskParseBook
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("bad payload: %v: %w", err, asynq.SkipRetry)
	}
	var book Book
	if err := db.First(&book, p.BookID).Error; err != nil {
		return fmt.Errorf("book %d not found: %w", p.BookID, err)
	}

	// Parse lock: a timed-out parse's goroutine/subprocess keeps running after
	// asynq gives up, so a retry could run resetBookContent (delete chunks)
	// while the first parse is still inserting → duplicate/corrupt chunk set,
	// book wedged in 'parsing'. Take a single-holder lock; if another parse
	// holds it, skip this retry (SkipRetry) rather than corrupt.
	if !claimParse(p.BookID) {
		log.Printf("⏭️ parse for book %d already in progress — skipping duplicate", p.BookID)
		return fmt.Errorf("parse already running: %w", asynq.SkipRetry)
	}
	defer releaseParse(p.BookID)

	db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "parsing")
	resetBookContent(p.BookID) // idempotent: clear any prior chunks on re-parse
	pages, err := ChunkDocumentBatch(p.BookID, book.FilePath)
	if err != nil {
		// Distinguish "no extractable text" (likely a scanned/image PDF) so the
		// client can show a tailored message; SkipRetry since retrying the same
		// textless file will never succeed.
		if errors.Is(err, errNoTextExtracted) {
			db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "no_text_extracted")
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "chunking_failed")
		return err
	}
	db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "pending")
	log.Printf("📖 Parsed book %d into %d pages (ready for transcription)", p.BookID, pages)
	return nil
}

// claimParse / releaseParse guard against concurrent parses of the same book
// (retry-while-orphaned-goroutine-still-running). 30-min TTL covers the worst
// legitimate parse; fails open if Redis is down.
func claimParse(bookID uint) bool {
	if rdb == nil {
		return true
	}
	key := fmt.Sprintf("parse:lock:%d", bookID)
	ok, err := rdb.SetNX(context.Background(), key, "1", 30*time.Minute).Result()
	if err != nil {
		return true
	}
	return ok
}

func releaseParse(bookID uint) {
	if rdb == nil {
		return
	}
	rdb.Del(context.Background(), fmt.Sprintf("parse:lock:%d", bookID))
}

// handleHLSPackage segments a completed page's audio into HLS and stores the
// playlist key on the chunk (idempotent). Runs as a follow-on so it never slows
// the listen-ready path.
func handleHLSPackage(ctx context.Context, t *asynq.Task) error {
	var p TaskHLSPackage
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("bad payload: %v: %w", err, asynq.SkipRetry)
	}
	var chunk BookChunk
	if err := db.Where("book_id = ? AND \"index\" = ?", p.BookID, p.PageIndex).First(&chunk).Error; err != nil {
		return err
	}
	if chunk.HLSPath != "" || chunk.FinalAudioPath == "" {
		return nil // already packaged, or no source yet
	}
	key, err := packageHLS(p.BookID, p.PageIndex, chunk.FinalAudioPath)
	if err != nil {
		return err
	}
	db.Model(&BookChunk{}).Where("id = ?", chunk.ID).Update("hls_path", key)
	log.Printf("🎞️ HLS packaged for book %d page %d → %s", p.BookID, p.PageIndex, key)
	return nil
}

// handleLookAhead transcribes + HLS-packages a small window of pages ahead of
// the listener so HLS (not the MP3 fallback) is what plays as they advance.
func handleLookAhead(ctx context.Context, t *asynq.Task) error {
	var p TaskLookAhead
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("bad payload: %v: %w", err, asynq.SkipRetry)
	}
	var book Book
	if err := db.First(&book, p.BookID).Error; err != nil {
		return err
	}
	endIndex := p.StartIndex + p.Count - 1
	var chunks []BookChunk
	db.Where("book_id = ? AND \"index\" BETWEEN ? AND ?", p.BookID, p.StartIndex, endIndex).
		Order("\"index\" ASC").Find(&chunks)
	for _, ch := range chunks {
		if ch.TTSStatus == "completed" {
			// Already transcribed — just make sure HLS is packaged.
			if ch.HLSPath == "" {
				if err := enqueueHLSPackage(p.BookID, ch.Index); err != nil {
					log.Printf("⚠️ lookahead HLS enqueue book %d page %d: %v", p.BookID, ch.Index, err)
				}
			}
			continue
		}
		if err := lookAheadTranscribeChunk(book, ch, p.UserID, p.AccountType); err != nil {
			if errors.Is(err, errQuotaExceeded) {
				log.Printf("🛑 lookahead quota reached for user %d book %d", p.UserID, p.BookID)
				break
			}
			log.Printf("⚠️ lookahead page %d (book %d) failed: %v", ch.Index, p.BookID, err)
		}
	}
	return nil
}

// lookAheadTranscribeChunk runs the SAME pipeline as the on-demand play path
// (TTS → music + Foley merge → HLS) for one page, synchronously, so look-ahead
// pages sound identical and are HLS-ready before the listener arrives. The
// atomic claim makes it idempotent and safe to race with the play path.
func lookAheadTranscribeChunk(book Book, chunk BookChunk, userID uint, accountType string) error {
	claim := db.Model(&BookChunk{}).
		Where("id = ? AND tts_status NOT IN ?", chunk.ID, []string{"processing", "completed"}).
		Update("tts_status", "processing")
	if claim.RowsAffected == 0 {
		return nil // already done or in-flight elsewhere
	}
	if d := checkAndConsume(userID, accountType, "transcribe_pages", 1, book.ID); !d.Allowed {
		db.Model(&BookChunk{}).Where("id = ?", chunk.ID).Update("tts_status", "pending")
		return errQuotaExceeded
	}
	audioPath, err := convertTextToAudioForChunk(chunk)
	if err != nil {
		db.Model(&BookChunk{}).Where("id = ?", chunk.ID).Update("tts_status", "failed")
		return err
	}
	db.Model(&BookChunk{}).Where("id = ?", chunk.ID).Updates(map[string]interface{}{
		"audio_path": audioPath,
		"tts_status": "completed",
	})
	// Synchronous merge (worker job owns it): sets final_audio_path + enqueues HLS.
	processSoundEffectsAndMerge(book, book.ContentHash, []int{chunk.Index})
	return nil
}

// reconcileUploadsLoop periodically completes/expires uploads that were
// initiated but never confirmed by the client (R2 has no event webhooks).
func reconcileUploadsLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		reconcileStaleUploads()
		reclaimStalePages()
		reclaimWedgedParses()
	}
}

// reclaimStalePages resets chunks stuck in tts_status='processing' longer than
// the batch timeout back to 'pending' so a timed-out/crashed batch doesn't lose
// those pages forever (the claim guard excludes 'processing', so they'd never
// be retried otherwise). Their consumed quota is metered, not refunded — cheap
// vs. permanently-dead pages.
func reclaimStalePages() {
	cutoff := time.Now().Add(-35 * time.Minute) // > batch Timeout (30m)
	res := db.Model(&BookChunk{}).
		Where("tts_status = ? AND updated_at < ?", "processing", cutoff).
		Update("tts_status", "pending")
	if res.RowsAffected > 0 {
		log.Printf("♻️ reclaimed %d page(s) stuck in 'processing'", res.RowsAffected)
	}
}

// reclaimWedgedParses catches books stuck in 'parsing' with zero chunks well
// past any legitimate parse window (parse lock TTL 30m) and marks them failed,
// so a client isn't left staring at a book that will never progress.
func reclaimWedgedParses() {
	cutoff := time.Now().Add(-40 * time.Minute)
	var books []Book
	if err := db.Where("status = ? AND updated_at < ?", "parsing", cutoff).Find(&books).Error; err != nil {
		return
	}
	for _, b := range books {
		var chunkCount int64
		db.Model(&BookChunk{}).Where("book_id = ?", b.ID).Count(&chunkCount)
		if chunkCount == 0 {
			db.Model(&Book{}).Where("id = ?", b.ID).Update("status", "chunking_failed")
			log.Printf("♻️ book %d wedged in 'parsing' with no chunks — marked chunking_failed", b.ID)
		}
	}
}

func reconcileStaleUploads() {
	cutoff := time.Now().Add(-15 * time.Minute)
	var books []Book
	if err := db.Where("status = ? AND updated_at < ?", "awaiting_upload", cutoff).Find(&books).Error; err != nil {
		return
	}
	for _, b := range books {
		if b.FilePath == "" {
			continue
		}
		ok, err := store.Exists(context.Background(), b.FilePath)
		if err != nil {
			continue
		}
		if ok {
			// Object arrived but the client never confirmed — finish it.
			db.Model(&Book{}).Where("id = ?", b.ID).Update("status", "parsing")
			if err := enqueueParseBook(b.ID); err != nil {
				log.Printf("⚠️ reconcile: enqueue parse for book %d failed: %v", b.ID, err)
			}
			log.Printf("♻️ reconcile: completed orphaned upload for book %d", b.ID)
		} else {
			db.Model(&Book{}).Where("id = ?", b.ID).Update("status", "upload_expired")
			log.Printf("♻️ reconcile: expired upload for book %d", b.ID)
		}
	}
}

// publishPagesReady emits an MQTT event telling the app how many pages are playable.
func publishPagesReady(book Book, pagesReady int) {
	payload, _ := json.Marshal(map[string]interface{}{
		"book_id":     book.ID,
		"pages_ready": pagesReady,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
	PublishEvent(fmt.Sprintf("users/%d/pages_ready", book.UserID), payload)
}
