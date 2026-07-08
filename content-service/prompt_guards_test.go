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
	got := resolveEventTimestamps(text, 100.0, ev)
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
	got := resolveEventTimestamps(text, 60.0, ev)
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
	got := resolveEventTimestamps(text, 30.0, ev)
	if len(got) != 0 {
		t.Fatalf("hallucinated/invalid events must be dropped, got %v", got)
	}
}

func TestResolveEventTimestamps_ClampsNearEnd(t *testing.T) {
	text := "Silence. Then thunder rolled."
	ev := []foleyQuoteEvent{{Type: "thunder", Quote: "thunder rolled."}}
	got := resolveEventTimestamps(text, 10.0, ev)
	if len(got["thunder"]) != 1 || got["thunder"][0] > 9.5 {
		t.Fatalf("timestamp must clamp to leave room for the clip, got %v", got)
	}
}

func TestResolveEventTimestamps_OCRLineWrap(t *testing.T) {
	// OCR _djvu.txt hard-wraps lines; a quote spanning the wrap must still match.
	text := "it was a very dubious-looking, nay, a very dark and dismal\nnight, bitingly cold and cheerless. I knew no one in the place."
	ev := []foleyQuoteEvent{{Type: "wind", Quote: "a very dark and dismal night, bitingly cold and cheerless"}}
	got := resolveEventTimestamps(text, 60.0, ev)
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
	if changed := assignSegmentVoices(vm, chunk1); !changed {
		t.Fatal("first chunk must register new characters")
	}
	if chunk1[0].Voice == chunk1[2].Voice {
		t.Fatalf("two male characters must get distinct voices, both got %s", chunk1[0].Voice)
	}

	// Next chunk: same characters, model now guesses Darcy's gender wrong.
	chunk2 := []DialogueSegment{
		{Type: "dialogue", Speaker: "darcy", Gender: "unknown", IsDialogue: true, Text: "d"},
	}
	if changed := assignSegmentVoices(vm, chunk2); changed {
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
	assignSegmentVoices(vm, segs)
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
