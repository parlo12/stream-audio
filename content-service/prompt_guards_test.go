package main

// Tests for the audit Phase-1 correctness guards (TTS_AUDIO_PROMPT_AUDIT.md):
// segmentsCoverInput must accept faithful dialogue splits (including quote /
// punctuation reformatting) and reject dropped or paraphrased book text.

import "testing"

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
