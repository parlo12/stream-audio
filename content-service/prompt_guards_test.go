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
