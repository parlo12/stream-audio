package main

// Character-voice continuity (audit H1, TTS_AUDIO_PROMPT_AUDIT.md Phase 3).
//
// Problem: gender was re-guessed per 1,000-char chunk with no memory, only 3
// voices existed, and the same character could flip voices between pages.
//
// Model: each book persists a character → {gender, voice} map (books.voice_map,
// JSON). New characters get the next voice from a per-gender pool, round-robin;
// once assigned, a character keeps its voice for the whole book. The known cast
// is fed back into the dialogue-analysis prompt so the model reuses canonical
// speaker names ("Lizzy" → "Elizabeth") and stays consistent across chunks.

import (
	"encoding/json"
	"log"
	"sort"
	"strings"
)

// CharacterVoice is one persisted cast entry.
type CharacterVoice struct {
	Gender string `json:"gender"` // "male" | "female" | "unknown"
	Voice  string `json:"voice"`  // OpenAI TTS voice id
}

// Voice pools (gpt-4o-mini-tts voices). Narrator stays on VoiceNarrator; the
// pools deliberately exclude it so characters never share the narrator's voice.
var (
	maleVoicePool   = []string{"onyx", "echo", "ash"}
	femaleVoicePool = []string{"nova", "shimmer", "coral"}
	// unknownVoicePool serves NAMED characters whose gender the model can't
	// determine (God, the Serpent, "the voice"). Without it they all collapsed
	// onto the single fallback voice and sounded identical in the same scene.
	// Uses the gpt-4o-mini-tts voices unused by narrator/male/female pools.
	unknownVoicePool = []string{"fable", "verse", "ballad", "sage"}
)

// unknownDialogueVoice is used for dialogue with NO attributable speaker —
// distinct from the narrator so conversations don't collapse into narration
// (the old behavior sent unknown-gender dialogue to the narrator voice).
const unknownDialogueVoice = "fable"

// normalizeSpeaker canonicalizes a speaker name for map keys.
func normalizeSpeaker(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}

// placeholderSpeakers are non-names the model falls back to when it can't
// attribute a line (e.g. a page break hid the "said Mrs. Bennet" tag). They
// must NOT become cast members: "unknown male" previously slipped through as a
// named character, took a male-pool voice, and voiced women in a man's voice.
// Treat all of these as unattributed → the neutral UnknownVoice instead.
var placeholderSpeakers = map[string]bool{
	"": true, "unknown": true,
	"unknown male": true, "unknown female": true,
	"unknown man": true, "unknown woman": true,
	"unknown speaker": true, "unknown character": true,
	"man": true, "woman": true, "boy": true, "girl": true,
	"someone": true, "somebody": true, "person": true,
	"speaker": true, "voice": true, "narrator": true,
}

// isPlaceholderSpeaker reports whether a normalized speaker name is a
// non-attributable placeholder rather than a real character.
func isPlaceholderSpeaker(key string) bool {
	return placeholderSpeakers[key]
}

// loadVoiceMap reads the book's persisted cast (empty map if none).
func loadVoiceMap(bookID uint) map[string]CharacterVoice {
	var b Book
	if err := db.Select("voice_map").First(&b, bookID).Error; err != nil || strings.TrimSpace(b.VoiceMap) == "" {
		return map[string]CharacterVoice{}
	}
	vm := map[string]CharacterVoice{}
	if err := json.Unmarshal([]byte(b.VoiceMap), &vm); err != nil {
		log.Printf("⚠️ [VoiceMap] book %d: unparseable voice_map, starting fresh: %v", bookID, err)
		return map[string]CharacterVoice{}
	}
	return vm
}

// saveVoiceMap persists the cast. Read-merge-write: concurrent chunks of the
// same book could race, worst case re-assigning one new character once — the
// persisted value wins for all later chunks, so drift is self-healing.
func saveVoiceMap(bookID uint, vm map[string]CharacterVoice) {
	data, err := json.Marshal(vm)
	if err != nil {
		return
	}
	if err := db.Model(&Book{}).Where("id = ?", bookID).Update("voice_map", string(data)).Error; err != nil {
		log.Printf("⚠️ [VoiceMap] book %d: save failed: %v", bookID, err)
	}
}

// pickVoice returns the next round-robin voice for a gender, based on how many
// characters of that pool are already cast. Deterministic given the map.
func pickVoice(vm map[string]CharacterVoice, gender string, cfg *ttsEngineConfig) string {
	var pool []string
	switch strings.ToLower(gender) {
	case "male":
		pool = cfg.MalePool
	case "female":
		pool = cfg.FemalePool
	default:
		pool = cfg.UnknownPool
	}
	inPool := map[string]bool{}
	for _, v := range pool {
		inPool[v] = true
	}
	n := 0
	for _, cv := range vm {
		if inPool[cv.Voice] {
			n++
		}
	}
	return pool[n%len(pool)]
}

// assignSegmentVoices gives every dialogue segment a stable per-character
// voice, updating the cast with newly met characters. Returns true if the cast
// changed (caller persists). First-seen gender wins for a character — a later
// contradictory guess must not flip an already-assigned voice.
func assignSegmentVoices(vm map[string]CharacterVoice, segments []DialogueSegment, cfg *ttsEngineConfig) bool {
	changed := false
	for i := range segments {
		s := &segments[i]
		if !s.IsDialogue {
			continue
		}
		key := normalizeSpeaker(s.Speaker)
		if isPlaceholderSpeaker(key) {
			// Unattributed line — neutral voice, no gender guess, never cast it.
			s.Voice = cfg.UnknownVoice
			s.Gender = "unknown"
			continue
		}
		cv, ok := vm[key]
		if !ok {
			cv = CharacterVoice{
				Gender: strings.ToLower(strings.TrimSpace(s.Gender)),
				Voice:  pickVoice(vm, s.Gender, cfg),
			}
			vm[key] = cv
			changed = true
			log.Printf("🎭 [VoiceMap] New character %q (%s) → voice %s", s.Speaker, cv.Gender, cv.Voice)
		}
		s.Voice = cv.Voice
		if cv.Gender != "" {
			s.Gender = cv.Gender // continuity beats this chunk's re-guess
		}
	}
	return changed
}

// castPromptSection renders the known cast for the dialogue-analysis prompt
// (capped so long books don't bloat the prompt).
func castPromptSection(vm map[string]CharacterVoice) string {
	if len(vm) == 0 {
		return "None yet."
	}
	names := make([]string, 0, len(vm))
	for name := range vm {
		names = append(names, name)
	}
	// Deterministic order (audit L1) and a hard cap.
	sort.Strings(names)
	if len(names) > 30 {
		names = names[:30]
	}
	var b strings.Builder
	for _, n := range names {
		b.WriteString("- ")
		b.WriteString(n)
		b.WriteString(" (")
		if g := vm[n].Gender; g != "" {
			b.WriteString(g)
		} else {
			b.WriteString("unknown")
		}
		b.WriteString(")\n")
	}
	return b.String()
}

// prevChunkTail returns the last maxRunes of the preceding chunk's text — fed
// to dialogue analysis as attribution context ("she replied" needs to know who
// spoke last page). Empty for the first chunk or on any error.
func prevChunkTail(bookID uint, index int, maxRunes int) string {
	if index <= 0 {
		return ""
	}
	var prev BookChunk
	if err := db.Select("content").
		Where("book_id = ? AND \"index\" = ?", bookID, index-1).
		First(&prev).Error; err != nil {
		return ""
	}
	r := []rune(prev.Content)
	if len(r) > maxRunes {
		r = r[len(r)-maxRunes:]
	}
	return string(r)
}
