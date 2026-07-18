package main

// Per-book TTS voice engine (July 18, 2026).
//
// Blind bake-off (AI_PIPELINE_ARCHITECTURE_ANALYSIS.md): Kokoro-82M beat
// OpenAI gpt-4o-mini-tts and Eleven v3 on the P&P Bennet passage, at
// ~$0.04/audio-hour hosted (vs $0.90 OpenAI, $4.76 Eleven v3).
//
// Model: books.tts_engine pins the engine for a book's whole lifetime —
// existing books stay on the engine that voiced them (voice continuity;
// switching would also demand a full re-render + HLS invalidation). New
// books get DEFAULT_TTS_ENGINE. The same seam is where the premium
// "Cinematic+ voices" toggle will plug in an ElevenLabs engine later.
//
// Kokoro is served through DeepInfra's OpenAI-compatible /audio/speech
// endpoint, so both engines share one request shape; only endpoint, key,
// model, voice names, and instructions support differ.

import (
	"os"
	"strings"
)

// ttsEngineConfig describes one voice engine.
type ttsEngineConfig struct {
	Name                 string
	Endpoint             string
	APIKey               func() string
	Model                string
	SupportsInstructions bool // OpenAI instructions field (emotion steering)
	NarratorVoice        string
	UnknownVoice         string   // unnamed speech fallback
	MalePool             []string // round-robin per-character pools
	FemalePool           []string
	UnknownPool          []string // named characters of unknown gender
}

var openaiEngine = ttsEngineConfig{
	Name:                 "openai",
	Endpoint:             openaiTTSEndpoint,
	APIKey:               func() string { return os.Getenv("OPENAI_API_KEY") },
	Model:                "gpt-4o-mini-tts",
	SupportsInstructions: true,
	NarratorVoice:        VoiceNarrator,
	UnknownVoice:         unknownDialogueVoice,
	MalePool:             maleVoicePool,
	FemalePool:           femaleVoicePool,
	UnknownPool:          unknownVoicePool,
}

// Kokoro British cast mirrors the winning bake-off sample (bm_george
// narrator, bm_lewis / bf_emma leads).
var kokoroEngine = ttsEngineConfig{
	Name: "kokoro",
	Endpoint: func() string {
		if v := os.Getenv("KOKORO_API_URL"); v != "" {
			return v
		}
		return "https://api.deepinfra.com/v1/openai/audio/speech"
	}(),
	APIKey:               func() string { return os.Getenv("KOKORO_API_KEY") },
	Model:                envStr("KOKORO_MODEL", "hexgrad/Kokoro-82M"),
	SupportsInstructions: false,
	NarratorVoice:        "bm_george",
	UnknownVoice:         "bm_fable",
	MalePool:             []string{"bm_lewis", "am_michael", "am_fenrir"},
	FemalePool:           []string{"bf_emma", "af_heart", "bf_isabella"},
	UnknownPool:          []string{"bm_daniel", "af_nicole", "am_puck", "bf_alice"},
}

var ttsEngines = map[string]*ttsEngineConfig{
	"openai": &openaiEngine,
	"kokoro": &kokoroEngine,
}

// defaultTTSEngine is applied to NEWLY created books only.
func defaultTTSEngine() string {
	e := strings.ToLower(envStr("DEFAULT_TTS_ENGINE", "openai"))
	if _, ok := ttsEngines[e]; !ok {
		return "openai"
	}
	return e
}

// engineFor resolves a book's pinned engine; empty/unknown → openai
// (every book rendered before this feature was voiced by OpenAI).
func engineFor(book Book) *ttsEngineConfig {
	if cfg, ok := ttsEngines[strings.ToLower(strings.TrimSpace(book.TTSEngine))]; ok {
		return cfg
	}
	return &openaiEngine
}

// engineForBookID loads just the engine column; openai on any failure.
func engineForBookID(bookID uint) *ttsEngineConfig {
	if bookID == 0 {
		return &openaiEngine
	}
	var b Book
	if err := db.Select("tts_engine").First(&b, bookID).Error; err != nil {
		return &openaiEngine
	}
	return engineFor(b)
}
