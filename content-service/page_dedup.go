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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"time"
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

// engineName resolves the pinned engine name for dedup keying.
func engineName(book Book) string {
	return engineFor(book).Name
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

// reuseRenderedPageForChunk short-circuits a page render when identical
// text+engine was already rendered for any book. Returns true if the chunk
// was completed by reuse (caller must skip the pipeline). HLS is re-packaged
// per-book from the shared audio (cheap, no AI cost).
func reuseRenderedPageForChunk(book Book, chunk BookChunk) bool {
	hash := contentHash(chunk.Content)
	engine := engineName(book)
	rp, ok := lookupRenderedPage(hash, engine)
	if !ok {
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
