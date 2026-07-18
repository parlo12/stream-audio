package main

// Tests for the audit Phase-1 correctness guards (TTS_AUDIO_PROMPT_AUDIT.md):
// segmentsCoverInput must accept faithful dialogue splits (including quote /
// punctuation reformatting) and reject dropped or paraphrased book text.

import (
	"strings"
	"testing"
)

func seg(text string) DialogueSegment {
	return DialogueSegment{Type: "narrator", Text: text}
}

func TestSegmentsCoverInput_FaithfulSplitPasses(t *testing.T) {
	input := `The knight approached slowly. "Who goes there?" he demanded. "It is I, the princess."`
	segs := []DialogueSegment{
		seg("The knight approached slowly."),
		{Type: "dialogue", Speaker: "Knight", Gender: "male", Text: "Who goes there?", IsDialogue: true},
		seg("he demanded."),
		{Type: "dialogue", Speaker: "Princess", Gender: "female", Text: "It is I, the princess.", IsDialogue: true},
	}
	if !segmentsCoverInput(input, segs) {
		t.Fatal("faithful split should pass coverage check")
	}
}

func TestSegmentsCoverInput_PunctuationChangesIgnored(t *testing.T) {
	// Curly quotes, dashes, and ellipses differ — word content identical.
	input := `“Wait…” she said — quietly.`
	segs := []DialogueSegment{
		{Type: "dialogue", Text: "Wait...", IsDialogue: true},
		seg(`she said, quietly.`),
	}
	if !segmentsCoverInput(input, segs) {
		t.Fatal("punctuation-only differences should pass")
	}
}

func TestSegmentsCoverInput_DroppedSentenceFails(t *testing.T) {
	input := `The storm broke over the hills. Rain hammered the old roof for hours. ` +
		`Inside, Elena lit the last candle and waited for the thunder to pass. ` +
		`Her brother had not returned from the village since morning.`
	segs := []DialogueSegment{
		seg("The storm broke over the hills. Rain hammered the old roof for hours."),
		// The last two sentences were silently dropped by the model.
	}
	if segmentsCoverInput(input, segs) {
		t.Fatal("dropped sentences must fail coverage check")
	}
}

func TestSegmentsCoverInput_ParaphraseFails(t *testing.T) {
	input := `It is a truth universally acknowledged, that a single man in possession ` +
		`of a good fortune, must be in want of a wife.`
	segs := []DialogueSegment{
		// "Fixed" wording — classic silent-rewrite failure mode.
		seg("Everyone agrees that a rich bachelor must be looking for a wife."),
	}
	if segmentsCoverInput(input, segs) {
		t.Fatal("paraphrased text must fail coverage check")
	}
}

func TestSegmentsCoverInput_EmptyInputPasses(t *testing.T) {
	if !segmentsCoverInput("   ", nil) {
		t.Fatal("empty input should trivially pass")
	}
}

// ---- Phase 2: placement grounding (audit C2) ----

func TestSplitTextProportionally_ReconstructsAndBalances(t *testing.T) {
	text := "The rain fell steadily on the old station roof while Elena counted the minutes between trains and thought about the letter."
	for _, n := range []int{1, 2, 3, 5} {
		parts := splitTextProportionally(text, n)
		if len(parts) != n {
			t.Fatalf("n=%d: got %d parts", n, len(parts))
		}
		var joined string
		for _, p := range parts {
			joined += p
		}
		if joined != text {
			t.Fatalf("n=%d: concatenation does not reproduce input", n)
		}
	}
}

func TestResolveEventTimestamps_AnchorsProportionally(t *testing.T) {
	// Quote sits at the midpoint of the text → timestamp ≈ half the duration.
	text := "aaaa aaaa aaaa aaaa aaaa the door groaned open bbbb bbbb bbbb bbbb bbbb bbbb"
	ev := []foleyQuoteEvent{{Type: "door_creak", Quote: "the door groaned open"}}
	got := resolveEventTimestamps(text, 100.0, ev, nil)
	ts, ok := got["door_creak"]
	if !ok || len(ts) != 1 {
		t.Fatalf("expected one door_creak timestamp, got %v", got)
	}
	if ts[0] < 20 || ts[0] > 45 {
		t.Fatalf("expected timestamp near text position (~32s), got %.2f", ts[0])
	}
}

func TestResolveEventTimestamps_CurlyQuoteDrift(t *testing.T) {
	text := `He said “who goes there” — and drew his sword from its sheath.`
	ev := []foleyQuoteEvent{{Type: "sword_draw", Quote: `drew his sword from its sheath`}}
	got := resolveEventTimestamps(text, 60.0, ev, nil)
	if len(got["sword_draw"]) != 1 {
		t.Fatalf("curly-quote text should still match, got %v", got)
	}
}

func TestResolveEventTimestamps_UnfoundQuoteDropped(t *testing.T) {
	text := "A quiet afternoon in the library."
	ev := []foleyQuoteEvent{
		{Type: "explosion", Quote: "the building erupted in flames"}, // not in text
		{Type: "not_a_real_event", Quote: "quiet afternoon"},         // invalid type
	}
	got := resolveEventTimestamps(text, 30.0, ev, nil)
	if len(got) != 0 {
		t.Fatalf("hallucinated/invalid events must be dropped, got %v", got)
	}
}

func TestResolveEventTimestamps_ClampsNearEnd(t *testing.T) {
	text := "Silence. Then thunder rolled."
	ev := []foleyQuoteEvent{{Type: "thunder", Quote: "thunder rolled."}}
	got := resolveEventTimestamps(text, 10.0, ev, nil)
	if len(got["thunder"]) != 1 || got["thunder"][0] > 9.5 {
		t.Fatalf("timestamp must clamp to leave room for the clip, got %v", got)
	}
}

func TestResolveEventTimestamps_OCRLineWrap(t *testing.T) {
	// OCR _djvu.txt hard-wraps lines; a quote spanning the wrap must still match.
	text := "it was a very dubious-looking, nay, a very dark and dismal\nnight, bitingly cold and cheerless. I knew no one in the place."
	ev := []foleyQuoteEvent{{Type: "wind", Quote: "a very dark and dismal night, bitingly cold and cheerless"}}
	got := resolveEventTimestamps(text, 60.0, ev, nil)
	if len(got["wind"]) != 1 {
		t.Fatalf("quote spanning an OCR line wrap must match, got %v", got)
	}
}

// ---- Phase 3: voice continuity (audit H1) ----

func TestAssignSegmentVoices_StableAcrossChunks(t *testing.T) {
	vm := map[string]CharacterVoice{}
	chunk1 := []DialogueSegment{
		{Type: "dialogue", Speaker: "Darcy", Gender: "male", IsDialogue: true, Text: "a"},
		{Type: "dialogue", Speaker: "Elizabeth", Gender: "female", IsDialogue: true, Text: "b"},
		{Type: "dialogue", Speaker: "Bingley", Gender: "male", IsDialogue: true, Text: "c"},
	}
	if changed := assignSegmentVoices(vm, chunk1, &openaiEngine); !changed {
		t.Fatal("first chunk must register new characters")
	}
	if chunk1[0].Voice == chunk1[2].Voice {
		t.Fatalf("two male characters must get distinct voices, both got %s", chunk1[0].Voice)
	}

	// Next chunk: same characters, model now guesses Darcy's gender wrong.
	chunk2 := []DialogueSegment{
		{Type: "dialogue", Speaker: "darcy", Gender: "unknown", IsDialogue: true, Text: "d"},
	}
	if changed := assignSegmentVoices(vm, chunk2, &openaiEngine); changed {
		t.Fatal("known character must not change the cast")
	}
	if chunk2[0].Voice != chunk1[0].Voice {
		t.Fatalf("Darcy flipped voice across chunks: %s → %s", chunk1[0].Voice, chunk2[0].Voice)
	}
	if chunk2[0].Gender != "male" {
		t.Fatalf("persisted gender must win over re-guess, got %q", chunk2[0].Gender)
	}
}

func TestAssignSegmentVoices_UnknownSpeakerNotNarrator(t *testing.T) {
	vm := map[string]CharacterVoice{}
	segs := []DialogueSegment{
		{Type: "dialogue", Speaker: "", Gender: "unknown", IsDialogue: true, Text: "who is there"},
	}
	assignSegmentVoices(vm, segs, &openaiEngine)
	if segs[0].Voice == VoiceNarrator || segs[0].Voice == "" {
		t.Fatalf("unknown-speaker dialogue must not use the narrator voice, got %q", segs[0].Voice)
	}
}

func TestSegmentsCoverInput_ContextLeakFails(t *testing.T) {
	input := `"Good morning," said the captain.`
	segs := []DialogueSegment{
		// Model leaked the previous page's context into its output.
		seg("The ship had sailed at dawn under a heavy grey sky and the crew was uneasy."),
		{Type: "dialogue", Speaker: "Captain", Text: "Good morning,", IsDialogue: true},
		seg("said the captain."),
	}
	if segmentsCoverInput(input, segs) {
		t.Fatal("output much longer than input (context leak) must fail")
	}
}

func TestCastPromptSection_DeterministicAndCapped(t *testing.T) {
	vm := map[string]CharacterVoice{
		"zed": {Gender: "male", Voice: "onyx"},
		"amy": {Gender: "female", Voice: "nova"},
	}
	a, b := castPromptSection(vm), castPromptSection(vm)
	if a != b {
		t.Fatal("cast section must be deterministic")
	}
	if !strings.Contains(a, "amy (female)") || !strings.Contains(a, "zed (male)") {
		t.Fatalf("unexpected cast rendering: %q", a)
	}
	if strings.Index(a, "amy") > strings.Index(a, "zed") {
		t.Fatal("cast must be sorted")
	}
}

// ---- Phase 4: score palette (audit H2) ----

func TestParseScorePalette_RoundTrip(t *testing.T) {
	raw := `[{"mood":"neutral","prompt":"soft piano","r2_key":"audio/1/score/neutral.mp3"},{"mood":"action","prompt":"drums","r2_key":"audio/1/score/action.mp3"}]`
	cues := parseScorePalette(raw)
	if len(cues) != 2 || cues[0].Mood != "neutral" || cues[1].R2Key != "audio/1/score/action.mp3" {
		t.Fatalf("bad parse: %+v", cues)
	}
	if parseScorePalette("") != nil || parseScorePalette("not json") != nil || parseScorePalette("[]") != nil {
		t.Fatal("empty/invalid palettes must parse to nil")
	}
}

func TestCueForMood_Fallbacks(t *testing.T) {
	cues := []ScoreCue{{Mood: "neutral"}, {Mood: "action"}}
	if c, ok := cueForMood(cues, "action"); !ok || c.Mood != "action" {
		t.Fatal("exact mood must match")
	}
	if c, ok := cueForMood(cues, "climax"); !ok || c.Mood != "neutral" {
		t.Fatal("missing mood must fall back to neutral")
	}
	if c, ok := cueForMood([]ScoreCue{{Mood: "sad"}}, "action"); !ok || c.Mood != "sad" {
		t.Fatal("no neutral: must fall back to any cue")
	}
	if _, ok := cueForMood(nil, "action"); ok {
		t.Fatal("empty palette must report not-ok")
	}
}

func TestDefaultCuePrompt_CoversAllMoods(t *testing.T) {
	for _, m := range scoreMoods {
		if p := defaultCuePrompt(m); p == "" || !strings.Contains(strings.ToLower(p), "instrumental") {
			t.Fatalf("mood %s: weak default prompt %q", m, p)
		}
	}
}

// ---- Phase 5: catalog fit (audit H3/H4) ----

func TestCleanOCRText(t *testing.T) {
	in := "It was a beauti-\nful morning in the har-\nbour town.\n\n47\n\n[ 48 ]\nDigitized by Google\nhttps://archive.org/details/somebook\nThe ships lay at anchor.\n\n\n\n\nAll was quiet."
	got := cleanOCRText(in)
	for _, want := range []string{"beautiful", "harbour", "The ships lay at anchor."} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"beauti-", "Digitized", "archive.org", "\n47\n", "[ 48 ]", "\n\n\n"} {
		if strings.Contains(got, bad) {
			t.Fatalf("artifact %q survived cleanup:\n%s", bad, got)
		}
	}
}

func TestCleanOCRText_PreservesRealNumbers(t *testing.T) {
	in := "The year 1851 was hard.\nChapter 3 begins with 40 men on deck."
	got := cleanOCRText(in)
	if !strings.Contains(got, "1851") || !strings.Contains(got, "40 men") {
		t.Fatalf("in-sentence numbers must survive:\n%s", got)
	}
}

func TestParseAudioProfile_And_Hint(t *testing.T) {
	p := parseAudioProfile(`{"fiction":false,"genre":"history","era":"historical"}`)
	if p == nil || p.Fiction || p.Genre != "history" {
		t.Fatalf("bad parse: %+v", p)
	}
	hint := p.promptHint(Book{Title: "Decline and Fall", Author: "Gibbon"})
	if !strings.Contains(hint, "NONFICTION") || !strings.Contains(hint, "history") {
		t.Fatalf("bad hint: %q", hint)
	}
	if parseAudioProfile("") != nil || parseAudioProfile("junk") != nil {
		t.Fatal("empty/invalid profiles must parse to nil")
	}
}

func TestBuildTimingMap_CumulativeSpans(t *testing.T) {
	tm := buildTimingMap(
		[]string{"abcd", "efghij", "kl"},         // 4, 6, 2 runes (+1 join each)
		[]float64{2.0, 6.0, 1.0},
	)
	if len(tm) != 3 {
		t.Fatalf("want 3 spans, got %d", len(tm))
	}
	if tm[1].StartRune != 5 || tm[1].EndRune != 12 {
		t.Fatalf("segment 2 rune span wrong: %+v", tm[1])
	}
	if tm[2].StartSec != 8.0 || tm[2].EndSec != 9.0 {
		t.Fatalf("segment 3 time span wrong: %+v", tm[2])
	}
	if buildTimingMap(nil, nil) != nil || buildTimingMap([]string{"a"}, nil) != nil {
		t.Fatal("mismatched/empty inputs must produce nil map")
	}
}

func TestTimeForRuneOffset_InterpolatesWithinSegment(t *testing.T) {
	// Segment 1: runes 0-10 over 0-2s (fast). Segment 2: runes 10-20 over
	// 2-12s (slow). Proportional mapping would put rune 15 at 7.5s of a 10s
	// page — the map must place it mid-segment-2 at 7s.
	tm := []SegmentTiming{
		{StartRune: 0, EndRune: 10, StartSec: 0, EndSec: 2},
		{StartRune: 10, EndRune: 20, StartSec: 2, EndSec: 12},
	}
	if got := timeForRuneOffset(tm, 15, 20, 12.0); got != 7.0 {
		t.Fatalf("rune 15: want 7.0s, got %.2f", got)
	}
	if got := timeForRuneOffset(tm, 0, 20, 12.0); got != 0 {
		t.Fatalf("rune 0: want 0s, got %.2f", got)
	}
	// Past the end clamps to the last segment's end.
	if got := timeForRuneOffset(tm, 25, 20, 12.0); got != 12.0 {
		t.Fatalf("past-end: want 12.0s, got %.2f", got)
	}
	// No map: legacy proportional.
	if got := timeForRuneOffset(nil, 15, 20, 12.0); got != 9.0 {
		t.Fatalf("proportional fallback: want 9.0s, got %.2f", got)
	}
	// Searched-text length differs from map span: offsets rescale.
	if got := timeForRuneOffset(tm, 30, 40, 12.0); got != 7.0 {
		t.Fatalf("rescaled rune 30/40: want 7.0s, got %.2f", got)
	}
}

func TestStripVerseCitations(t *testing.T) {
	in := "Genesis 1:17\tAnd God set them in the firmament\nGenesis 1:18\tAnd to rule over the day\n1 Samuel 3:4\tThat the LORD called Samuel\nSong of Solomon 2:1\tI am the rose of Sharon"
	got := stripVerseCitations(in)
	for _, banned := range []string{"1:17", "1:18", "3:4", "2:1", "Genesis", "1 Samuel", "Solomon"} {
		if strings.Contains(got, banned) {
			t.Fatalf("citation fragment %q survived: %q", banned, got)
		}
	}
	for _, kept := range []string{"And God set them", "rule over the day", "LORD called Samuel", "rose of Sharon"} {
		if !strings.Contains(got, kept) {
			t.Fatalf("verse text %q lost: %q", kept, got)
		}
	}
	// prose without tab-separated citations is untouched, even with times
	prose := "The train left at 12:30 that day.\nShe said, \"wait for me.\""
	if stripVerseCitations(prose) != prose {
		t.Fatalf("prose was altered: %q", stripVerseCitations(prose))
	}
}

func TestPickVoice_NamedUnknownsGetDistinctVoices(t *testing.T) {
	vm := map[string]CharacterVoice{}
	segs := []DialogueSegment{
		{IsDialogue: true, Speaker: "God", Gender: "unknown", Text: "Let there be light"},
		{IsDialogue: true, Speaker: "Serpent", Gender: "unknown", Text: "Ye shall not surely die"},
	}
	assignSegmentVoices(vm, segs, &openaiEngine)
	if segs[0].Voice == segs[1].Voice {
		t.Fatalf("God and Serpent must not share a voice: both %q", segs[0].Voice)
	}
	// unnamed speech still falls back to the shared unknown voice
	anon := []DialogueSegment{{IsDialogue: true, Speaker: "", Gender: "unknown", Text: "hello"}}
	assignSegmentVoices(vm, anon, &openaiEngine)
	if anon[0].Voice != unknownDialogueVoice {
		t.Fatalf("unnamed speaker should use %q, got %q", unknownDialogueVoice, anon[0].Voice)
	}
}

func TestEnginePools_KokoroCastDistinct(t *testing.T) {
	vm := map[string]CharacterVoice{}
	segs := []DialogueSegment{
		{IsDialogue: true, Speaker: "Darcy", Gender: "male", Text: "a"},
		{IsDialogue: true, Speaker: "Elizabeth", Gender: "female", Text: "b"},
		{IsDialogue: true, Speaker: "God", Gender: "unknown", Text: "c"},
	}
	assignSegmentVoices(vm, segs, &kokoroEngine)
	seen := map[string]bool{}
	for _, s := range segs {
		if seen[s.Voice] {
			t.Fatalf("kokoro cast shares a voice: %+v", segs)
		}
		seen[s.Voice] = true
		if s.Voice == kokoroEngine.NarratorVoice {
			t.Fatalf("character got the narrator voice %q", s.Voice)
		}
	}
	// narrator resolution respects the engine
	n := getVoiceForSegment(DialogueSegment{Type: "narrator"}, &kokoroEngine)
	if n != "bm_george" {
		t.Fatalf("kokoro narrator: want bm_george, got %q", n)
	}
	if getVoiceForSegment(DialogueSegment{Type: "narrator"}, &openaiEngine) != VoiceNarrator {
		t.Fatal("openai narrator regressed")
	}
}

func TestUsesClassicalSpeech(t *testing.T) {
	bible := &AudioProfile{Fiction: true, Genre: "religious", Era: "ancient"}
	iliad := &AudioProfile{Fiction: true, Genre: "epic poetry", Era: "ancient"}
	scifi := &AudioProfile{Fiction: true, Genre: "science fiction", Era: "futuristic"}
	memoir := &AudioProfile{Fiction: false, Genre: "memoir", Era: "modern"}
	if !usesClassicalSpeech(bible, Book{}) || !usesClassicalSpeech(iliad, Book{}) {
		t.Fatal("scripture/epic must use classical speech rules")
	}
	if usesClassicalSpeech(scifi, Book{}) || usesClassicalSpeech(memoir, Book{}) {
		t.Fatal("modern books must keep quote-only rules")
	}
	// catalog fields count even when the classifier genre is bland
	if !usesClassicalSpeech(memoir, Book{Genre: "Norse Mythology"}) {
		t.Fatal("catalog genre must trigger classical speech")
	}
}

func TestIsCinematicGenre(t *testing.T) {
	cinematic := [][]string{
		{"religious"},               // Bible via classifier
		{"religion"},                // variant wording
		{"scripture", ""},           // explicit
		{"mythology"},               // Edda, Bulfinch
		{"epic poetry"},             // Iliad shelved as poetry/nonfiction
		{"", "Folklore & Legends"},  // catalog category, mixed case
		{"history", "Norse Sagas"},  // classifier says history, catalog knows better
	}
	for _, fields := range cinematic {
		if !isCinematicGenre(fields...) {
			t.Errorf("expected cinematic: %v", fields)
		}
	}
	flat := [][]string{
		{"history"},
		{"biography", ""},
		{"self-help"},
		{"business", "Reference"},
		{""},
	}
	for _, fields := range flat {
		if isCinematicGenre(fields...) {
			t.Errorf("expected NOT cinematic: %v", fields)
		}
	}
}
