package main

// Per-page segment timing map (audit Phase 2B — the C2 "proper fix").
//
// The multi-voice path renders one MP3 per dialogue segment. Recording each
// segment's rune span and measured duration yields a text-position →
// audio-time table, persisted on book_chunks.timing_map. Foley quote
// anchoring then interpolates WITHIN the segment containing the quote
// instead of assuming one uniform speaking rate across the whole page —
// narration, shouted dialogue, and whispered lines all run at different
// rates. Pages without a map (single-voice fallback, pre-existing audio)
// keep the legacy proportional mapping.

import (
	"encoding/json"
	"log"
	"strings"
	"unicode/utf8"
)

// SegmentTiming is one segment's span in the spoken text and the audio.
// Rune offsets are over the single-space-joined concatenation of the spoken
// segment texts (mirroring merge order).
type SegmentTiming struct {
	StartRune int     `json:"sr"`
	EndRune   int     `json:"er"`
	StartSec  float64 `json:"ss"`
	EndSec    float64 `json:"es"`
}

// buildTimingMap accumulates rune spans and measured durations. texts and
// durs must be parallel (only successfully rendered + measured segments).
func buildTimingMap(texts []string, durs []float64) []SegmentTiming {
	if len(texts) == 0 || len(texts) != len(durs) {
		return nil
	}
	tm := make([]SegmentTiming, 0, len(texts))
	runePos, sec := 0, 0.0
	for i, t := range texts {
		n := utf8.RuneCountInString(t) + 1 // +1: joining space between segments
		tm = append(tm, SegmentTiming{
			StartRune: runePos, EndRune: runePos + n,
			StartSec: sec, EndSec: sec + durs[i],
		})
		runePos += n
		sec += durs[i]
	}
	return tm
}

// timeForRuneOffset maps a rune offset in the searched page text to audio
// seconds. With a map: linear interpolation inside the containing segment
// (offsets are rescaled when the searched text's length differs slightly
// from the map's span — model whitespace drift, dropped segments). Without
// one: the legacy whole-page proportional estimate.
func timeForRuneOffset(tm []SegmentTiming, runeOff, totalRunes int, ttsDur float64) float64 {
	if totalRunes <= 0 {
		return 0
	}
	if len(tm) == 0 {
		return float64(runeOff) / float64(totalRunes) * ttsDur
	}
	if mapTotal := tm[len(tm)-1].EndRune; mapTotal > 0 && mapTotal != totalRunes {
		runeOff = runeOff * mapTotal / totalRunes
	}
	for _, s := range tm {
		if runeOff < s.EndRune {
			frac := 0.0
			if s.EndRune > s.StartRune {
				frac = float64(runeOff-s.StartRune) / float64(s.EndRune-s.StartRune)
			}
			return s.StartSec + frac*(s.EndSec-s.StartSec)
		}
	}
	return tm[len(tm)-1].EndSec
}

// saveTimingMap persists a chunk's map (no-op for empty maps).
func saveTimingMap(chunkID uint, tm []SegmentTiming) {
	if len(tm) == 0 {
		return
	}
	data, err := json.Marshal(tm)
	if err != nil {
		return
	}
	if err := db.Model(&BookChunk{}).Where("id = ?", chunkID).
		Update("timing_map", string(data)).Error; err != nil {
		log.Printf("⚠️ [Timing] chunk %d: save failed: %v", chunkID, err)
	}
}

// loadTimingMap returns a page's persisted map, nil when absent/invalid.
func loadTimingMap(bookID uint, index int) []SegmentTiming {
	var ch BookChunk
	if err := db.Select("timing_map").
		Where("book_id = ? AND \"index\" = ?", bookID, index).
		First(&ch).Error; err != nil || strings.TrimSpace(ch.TimingMap) == "" {
		return nil
	}
	var tm []SegmentTiming
	if err := json.Unmarshal([]byte(ch.TimingMap), &tm); err != nil {
		return nil
	}
	return tm
}
