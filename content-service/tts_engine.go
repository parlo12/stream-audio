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
// books get DEFAULT_TTS_ENGINE. The registry is open for additional engines,
// but there is no user-facing engine switch — the default is chosen at the
// platform level, not per user.
//
// Kokoro is served through DeepInfra's OpenAI-compatible /audio/speech
// endpoint, so both engines share one request shape; only endpoint, key,
// model, voice names, and instructions support differ.

import (
	"os"
	"strings"
)

// firstNonEmpty returns the first non-blank string, or "" if all are blank.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ttsEngineConfig describes one voice engine.
type ttsEngineConfig struct {
	Name string
	// Provider selects the request/response protocol: "" or "openai" for the
	// OpenAI-compatible /audio/speech shape (OpenAI, Kokoro via DeepInfra);
	// "elevenlabs" for the ElevenLabs /text-to-speech/{voice_id} shape.
	Provider             string
	Endpoint             string
	APIKey               func() string
	Model                string
	SupportsInstructions bool // OpenAI instructions field (emotion steering)
	ExpandTitles         bool // expand "Mr."→"Mister" etc. — Kokoro pauses on abbrev periods
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
	ExpandTitles:         true, // Kokoro reads "Mr." as a sentence end → dead pause
	NarratorVoice:        "bm_george",
	UnknownVoice:         "bm_fable",
	MalePool:             []string{"bm_lewis", "am_michael", "am_fenrir"},
	FemalePool:           []string{"bf_emma", "af_heart", "bf_isabella"},
	UnknownPool:          []string{"bm_daniel", "af_nicole", "am_puck", "bf_alice"},
}

// ElevenLabs v3 — the premium expressive engine, used for CHARACTER voices in
// the top-tier hybrid (Kokoro narration + Eleven dialogue). Different protocol
// than OpenAI/Kokoro: per-voice endpoint, xi-api-key header, emotion via inline
// audio tags ([sad], [whispers], [shouts]) rather than an instructions field.
// Voice ids below are the classic premade voices (stable, on every account);
// override per pool via env, and confirm against GET /v1/voices on activation.
var (
	elevenMalePool = []string{
		firstNonEmpty(os.Getenv("ELEVEN_MALE_1"), "pNInz6obpgDQGcFmaJgB"), // Adam
		firstNonEmpty(os.Getenv("ELEVEN_MALE_2"), "TxGEqnHWrfWFTfGW9XjX"), // Josh
		firstNonEmpty(os.Getenv("ELEVEN_MALE_3"), "VR6AewLTigWG4xSOukaG"), // Arnold
		firstNonEmpty(os.Getenv("ELEVEN_MALE_4"), "ErXwobaYiN019PkySvjV"), // Antoni
	}
	elevenFemalePool = []string{
		firstNonEmpty(os.Getenv("ELEVEN_FEMALE_1"), "21m00Tcm4TlvDq8ikWAM"), // Rachel
		firstNonEmpty(os.Getenv("ELEVEN_FEMALE_2"), "EXAVITQu4vr4xnSDxMaL"), // Bella
		firstNonEmpty(os.Getenv("ELEVEN_FEMALE_3"), "MF3mGyEYCl7XYWbV9V6O"), // Elli
		firstNonEmpty(os.Getenv("ELEVEN_FEMALE_4"), "AZnzlk1XvdvUeBnXmlld"), // Domi
	}
	elevenUnknownPool = []string{
		firstNonEmpty(os.Getenv("ELEVEN_UNKNOWN_1"), "yoZ06aMxZJJ28mfd3POQ"), // Sam
		firstNonEmpty(os.Getenv("ELEVEN_UNKNOWN_2"), "ThT5KcBeYPX3keUQqHPh"), // Dorothy
		firstNonEmpty(os.Getenv("ELEVEN_UNKNOWN_3"), "IKne3meq5aSn9XLyUdCD"), // Charlie
	}
)

var elevenEngine = ttsEngineConfig{
	Name:                 "eleven",
	Provider:             "elevenlabs",
	Endpoint:             envStr("ELEVEN_TTS_ENDPOINT", "https://api.elevenlabs.io/v1/text-to-speech"),
	APIKey:               func() string { return os.Getenv("ELEVENLABS_API_KEY") },
	Model:                envStr("ELEVEN_MODEL", "eleven_v3"),
	SupportsInstructions: false, // emotion via inline audio tags, not a prose field
	ExpandTitles:         false, // Eleven reads "Mr." naturally; keep author text intact
	NarratorVoice:        firstNonEmpty(os.Getenv("ELEVEN_NARRATOR_VOICE"), "pNInz6obpgDQGcFmaJgB"),
	UnknownVoice:         firstNonEmpty(os.Getenv("ELEVEN_UNKNOWN_VOICE"), "yoZ06aMxZJJ28mfd3POQ"),
	MalePool:             elevenMalePool,
	FemalePool:           elevenFemalePool,
	UnknownPool:          elevenUnknownPool,
}

var ttsEngines = map[string]*ttsEngineConfig{
	"openai": &openaiEngine,
	"kokoro": &kokoroEngine,
	"eleven": &elevenEngine,
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

// hybridDialogueEngine returns the engine to render DIALOGUE segments on when
// hybrid narration/dialogue rendering is enabled, or nil for no split (dialogue
// renders on the book's base engine). Narration ALWAYS uses the base engine.
//
// Rationale: Kokoro is ~$0.04/audio-hr but flat; an instruction-capable engine
// (OpenAI gpt-4o-mini-tts) gives characters real emotional timbre. Since a book
// is mostly narration, routing only the ~40% dialogue to the pricier engine
// keeps cost low (~$6/novel vs ~$54 all-OpenAI) while the characters come alive.
//
// Enabled globally via HYBRID_DIALOGUE_ENGINE (e.g. "openai"). Returns nil when
// unset, unknown, or equal to the base engine (an all-OpenAI book needs no
// split). The choice is global so cross-user dedup stays coherent — every book
// on a given base engine renders dialogue the same way.
func hybridDialogueEngine(base *ttsEngineConfig) *ttsEngineConfig {
	name := strings.ToLower(strings.TrimSpace(os.Getenv("HYBRID_DIALOGUE_ENGINE")))
	if name == "" || base == nil || name == base.Name {
		return nil
	}
	if cfg, ok := ttsEngines[name]; ok {
		return cfg
	}
	return nil
}
