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

	audioPath, err := convertTextToAudio(chunk.Content, chunk.ID)
	if err != nil {
		fail()
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(chunk.Content)))
	bgPrompt, err := generateOverallSoundPrompt(chunk.Content)
	if err != nil {
		fail()
		return err
	}
	bgMusic, err := getOrGenerateBackgroundMusic(bgPrompt)
	if err != nil {
		fail()
		return err
	}
	mergedAudio, err := mergeAudio(audioPath, bgMusic, book, chunk.Index, chunk.Content, hash)
	if err != nil {
		fail()
		return err
	}
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
	})
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

	// Auto-enqueue the next batch if there's more to do (and not quota-capped).
	var pendingBeyond int64
	db.Model(&BookChunk{}).Where("book_id = ? AND \"index\" > ? AND tts_status <> ?", p.BookID, p.EndPage, "completed").Count(&pendingBeyond)
	if !capped && pendingBeyond > 0 {
		// Pause-ahead: for free users, don't transcribe more than
		// PAUSE_AHEAD_PAGES beyond where they're currently listening. Resumed by
		// UpdatePlaybackProgressHandler when the listener advances.
		nextStart := p.EndPage + 1
		if p.AccountType == "free" && nextStart > listenerChunkIndex(p.UserID, p.BookID)+pauseAheadPages() {
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
	resetBookContent(p.BookID) // idempotent: clear any prior chunks on re-parse
	pages, err := ChunkDocumentBatch(p.BookID, book.FilePath)
	if err != nil {
		db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "chunking_failed")
		return err
	}
	db.Model(&Book{}).Where("id = ?", p.BookID).Update("status", "pending")
	log.Printf("📖 Parsed book %d into %d pages (ready for transcription)", p.BookID, pages)
	return nil
}

// reconcileUploadsLoop periodically completes/expires uploads that were
// initiated but never confirmed by the client (R2 has no event webhooks).
func reconcileUploadsLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		reconcileStaleUploads()
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
