package main

// Cross-user page-audio dedup (biggest remaining cost lever).
//
// Identical page text rendered by the same voice engine produces an
// equivalent audiobook page for every user — but until now each book
// re-ran the full pipeline (TTS + dialogue brain + classifiers + foley +
// music) and stored its own copy keyed by book ID. A popular free classic
// imported by 100 users paid 100× for identical work.
//
// Model: the mixed final audio is stored at a content-addressed SHARED key
// (shared/audio/{engine}/{sha256}.mp3) and registered in rendered_pages,
// keyed on (content_hash, engine). Before rendering a page, look it up; on a
// hit, point the chunk at the shared audio, adopt its cast, and skip the
// entire expensive pipeline. HLS is still packaged per-book (cheap CPU, no
// AI), so book deletion — which sweeps only audio/{book}/ — never touches
// shared audio. Shared objects are never deleted here (cheap, reusable; a
// ref-counted GC can come later).

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// RenderedPage maps a page's (text, engine) to its shared audio object.
type RenderedPage struct {
	ID uint `gorm:"primaryKey"`
	// One row per unique (content_hash, engine).
	ContentHash string    `gorm:"size:64;uniqueIndex:idx_rendered_page,priority:1"`
	Engine      string    `gorm:"size:32;uniqueIndex:idx_rendered_page,priority:2"`
	AudioKey    string    `gorm:"size:255"`      // shared R2 key of the mixed final audio
	VoiceMap    string    `gorm:"type:text"`     // cast used, so reusers stay consistent
	CreatedAt   time.Time
}

// contentHash is the full sha256 hex of a chunk's exact text.
func contentHash(text string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
}

// sharedAudioKey is the book-independent R2 key for a rendering. Book delete
// sweeps only audio/{book}/, so this survives to serve other books.
func sharedAudioKey(engine, hash, ext string) string {
	return fmt.Sprintf("shared/audio/%s/%s%s", engine, hash, ext)
}

// renderVersion namespaces the shared cache. Bump it whenever a rendering
// change must invalidate cached audio so pages re-render with the new pipeline
// instead of reusing stale audio. v2 = title-abbreviation pause fix (Jul 2026:
// "Mr." no longer inserts a dead pause). Old-version shared objects orphan and
// are reaped by the GC.
const renderVersion = "2"

// engineName resolves the pinned engine name.
func engineName(book Book) string {
	return engineFor(book).Name
}

// dedupEngineKey is the engine identity used for the shared cache — engine
// name plus render version, so a pipeline change starts a fresh namespace.
func dedupEngineKey(book Book) string {
	return engineName(book) + "-r" + renderVersion
}

// loadVoiceMapJSON returns the book's persisted voice_map as a raw JSON
// string (empty string if none) for storing alongside a shared rendering.
func loadVoiceMapJSON(bookID uint) string {
	var b Book
	if err := db.Select("voice_map").First(&b, bookID).Error; err != nil {
		return ""
	}
	return b.VoiceMap
}

// lookupRenderedPage finds a prior rendering of this exact text+engine.
func lookupRenderedPage(hash, engine string) (*RenderedPage, bool) {
	var rp RenderedPage
	if err := db.Where("content_hash = ? AND engine = ?", hash, engine).First(&rp).Error; err != nil {
		return nil, false
	}
	if rp.AudioKey == "" {
		return nil, false
	}
	return &rp, true
}

// registerRenderedPage records a fresh rendering so later books reuse it.
// Idempotent: a concurrent duplicate insert loses harmlessly (both point at
// equivalent audio for the same text).
func registerRenderedPage(hash, engine, audioKey, voiceMapJSON string) {
	rp := RenderedPage{ContentHash: hash, Engine: engine, AudioKey: audioKey, VoiceMap: voiceMapJSON}
	if err := db.Where("content_hash = ? AND engine = ?", hash, engine).
		FirstOrCreate(&rp, rp).Error; err != nil {
		log.Printf("⚠️ [Dedup] register failed for %s/%s: %v", engine, hash[:8], err)
	}
}

// adoptSharedCast merges a shared rendering's cast into the book so pages
// rendered fresh later use the same character voices. Existing book entries
// win (never re-map a character already voiced); shared entries fill gaps.
func adoptSharedCast(bookID uint, sharedVoiceMap string) {
	if sharedVoiceMap == "" {
		return
	}
	var shared map[string]CharacterVoice
	if json.Unmarshal([]byte(sharedVoiceMap), &shared) != nil || len(shared) == 0 {
		return
	}
	vm := loadVoiceMap(bookID)
	changed := false
	for k, cv := range shared {
		if _, ok := vm[k]; !ok {
			vm[k] = cv
			changed = true
		}
	}
	if changed {
		saveVoiceMap(bookID, vm)
	}
}

// gcOrphanedSharedRenderings removes shared renderings no book references
// anymore (every book that used them was deleted). A rendering is orphaned
// when zero book_chunks point final_audio_path at its shared key. Only
// entries older than graceMinutes are considered, so a rendering that's
// momentarily between "registered" and "referenced" isn't reaped. Deletes at
// most `limit` per run. The reuse path HEAD-checks the object (self-heal), so
// a rare race where a reuse attaches just as GC deletes resolves to a
// re-render, never a permanent 404. GC is the ONLY authorized deleter of
// shared/ objects — it calls store.Delete directly (deleteStored no-ops them).
func gcOrphanedSharedRenderings(graceMinutes, limit int) (int, error) {
	cutoff := time.Now().Add(-time.Duration(graceMinutes) * time.Minute)
	var candidates []RenderedPage
	if err := db.Where("created_at < ?", cutoff).Limit(limit).Find(&candidates).Error; err != nil {
		return 0, err
	}
	removed := 0
	for _, rp := range candidates {
		var refs int64
		if err := db.Model(&BookChunk{}).
			Where("final_audio_path = ?", rp.AudioKey).Count(&refs).Error; err != nil {
			continue
		}
		if refs > 0 {
			continue // still in use
		}
		// Drop the row first so no new reuse can attach to it, then delete the
		// object. store.Delete (not deleteStored) — GC is the shared deleter.
		if err := db.Delete(&RenderedPage{}, rp.ID).Error; err != nil {
			continue
		}
		if err := store.Delete(context.Background(), rp.AudioKey); err != nil {
			log.Printf("⚠️ [GC] could not delete shared object %s: %v", rp.AudioKey, err)
			continue
		}
		removed++
	}
	if removed > 0 {
		log.Printf("🧹 [GC] removed %d orphaned shared rendering(s)", removed)
	}
	return removed, nil
}

// gcSharedAudioHandler (admin) runs the orphan sweep on demand. Optional
// ?grace_minutes= override (default 60); returns how many were removed.
func gcSharedAudioHandler(c *gin.Context) {
	grace := envIntQuery(c, "grace_minutes", 60, 1_000_000)
	removed, err := gcOrphanedSharedRenderings(grace, 5000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"removed": removed})
}

// sharedAudioGCLoop runs the orphan sweep once a day in the worker.
func sharedAudioGCLoop() {
	interval := time.Duration(envInt("SHARED_GC_INTERVAL_MINUTES", 1440)) * time.Minute
	grace := envInt("SHARED_GC_GRACE_MINUTES", 60)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := gcOrphanedSharedRenderings(grace, 1000); err != nil {
			log.Printf("⚠️ [GC] sweep failed: %v", err)
		}
	}
}

// reuseRenderedPageForChunk short-circuits a page render when identical
// text+engine was already rendered for any book. Returns true if the chunk
// was completed by reuse (caller must skip the pipeline). HLS is re-packaged
// per-book from the shared audio (cheap, no AI cost).
func reuseRenderedPageForChunk(book Book, chunk BookChunk) bool {
	hash := contentHash(chunk.Content)
	engine := dedupEngineKey(book)
	rp, ok := lookupRenderedPage(hash, engine)
	if !ok {
		return false
	}
	// Self-heal: the shared object may be missing (deleted before the
	// no-delete guard existed, or a future GC). Verify it's really there
	// before pointing a chunk at it; if gone, drop the stale row and fall
	// through to a fresh render that re-creates and re-registers it.
	if exists, err := store.Exists(context.Background(), rp.AudioKey); err != nil || !exists {
		db.Where("content_hash = ? AND engine = ?", hash, engine).Delete(&RenderedPage{})
		log.Printf("🩹 [Dedup] stale shared %s (%s) missing — re-rendering", engine, hash[:8])
		return false
	}
	adoptSharedCast(book.ID, rp.VoiceMap)
	if err := db.Model(&BookChunk{}).Where("id = ?", chunk.ID).Updates(map[string]interface{}{
		"audio_path":       rp.AudioKey,
		"final_audio_path": rp.AudioKey,
		"tts_status":       "completed",
		"hls_path":         "", // re-package HLS per book below
	}).Error; err != nil {
		log.Printf("⚠️ [Dedup] chunk update failed for book %d page %d: %v", book.ID, chunk.Index, err)
		return false
	}
	log.Printf("♻️ [Dedup] book %d page %d reused shared %s rendering (%s) — pipeline skipped",
		book.ID, chunk.Index, engine, hash[:8])
	if err := enqueueHLSPackage(book.ID, chunk.Index); err != nil {
		log.Printf("⚠️ [Dedup] HLS enqueue failed for book %d page %d: %v", book.ID, chunk.Index, err)
	}
	return true
}
