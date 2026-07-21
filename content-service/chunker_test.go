package main

import (
	"strings"
	"testing"
	"unicode"
)

func TestWordSafeChunks_NoMidWordSplits(t *testing.T) {
	// Build ~5000 runes of realistic prose (words + spaces).
	words := strings.Fields(strings.Repeat(
		"However little known the feelings or views of such a man may be on his first entering a neighbourhood ", 60))
	text := strings.Join(words, " ")
	runes := []rune(text)
	spans := wordSafeChunks(runes, 1000)

	if len(spans) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(spans))
	}
	// 1) Every boundary lands on a word edge: the last rune of a chunk is a
	//    letter and the first rune of the next is a letter, with the dropped
	//    whitespace in between (i.e. chunk text has no trailing/leading space).
	for i := 0; i < len(spans)-1; i++ {
		s, e := spans[i][0], spans[i][1]
		chunk := string(runes[s:e])
		if chunk != strings.TrimRight(chunk, " \t\n") {
			t.Fatalf("chunk %d ends with whitespace (should end on a word): %q", i, chunk[len(chunk)-10:])
		}
		next := string(runes[spans[i+1][0]:spans[i+1][1]])
		// reconstruct the split word: last token of chunk + first token of next
		// must each be a real dictionary-ish token, not a fragment join.
		lastTok := chunk[strings.LastIndex(chunk, " ")+1:]
		firstTok := next[:strings.IndexFunc(next, unicode.IsSpace)]
		if lastTok == "" || firstTok == "" {
			t.Fatalf("empty boundary token at chunk %d", i)
		}
	}
	// 2) Chunks are near the target size (not wildly short from over-eager cuts).
	for i := 0; i < len(spans)-1; i++ {
		n := spans[i][1] - spans[i][0]
		if n < 1000-200 || n > 1000 {
			t.Fatalf("chunk %d size %d outside [800,1000]", i, n)
		}
	}
	// 3) Full coverage minus inter-chunk whitespace: concatenation reproduces
	//    the text with single spaces collapsed at boundaries.
	var joined strings.Builder
	for i, sp := range spans {
		if i > 0 {
			joined.WriteByte(' ')
		}
		joined.WriteString(string(runes[sp[0]:sp[1]]))
	}
	if strings.Join(strings.Fields(joined.String()), " ") != strings.Join(strings.Fields(text), " ") {
		t.Fatal("word-level reconstruction differs from input")
	}
}

func TestWordSafeChunks_LongTokenHardCut(t *testing.T) {
	// A 3000-rune run with no whitespace must still split (fallback), not hang.
	runes := []rune(strings.Repeat("x", 3000))
	spans := wordSafeChunks(runes, 1000)
	if len(spans) != 3 {
		t.Fatalf("no-whitespace blob: expected 3 hard-cut chunks, got %d", len(spans))
	}
	if spans[len(spans)-1][1] != 3000 {
		t.Fatal("last span must reach the end")
	}
}

func TestWordSafeChunks_ShortInput(t *testing.T) {
	if wordSafeChunks([]rune("just a short line"), 1000) == nil {
		t.Fatal("short input should yield one span")
	}
	if wordSafeChunks(nil, 1000) != nil {
		t.Fatal("empty input must be nil")
	}
}

func TestContentHash_DeterministicAndDistinct(t *testing.T) {
	a := contentHash("It is a truth universally acknowledged.")
	b := contentHash("It is a truth universally acknowledged.")
	c := contentHash("It is a truth universally acknowledged!")
	if a != b {
		t.Fatal("identical text must hash identically (cross-book dedup key)")
	}
	if a == c {
		t.Fatal("different text must hash differently")
	}
	if len(a) != 64 {
		t.Fatalf("expected 64-hex sha256, got %d chars", len(a))
	}
}

func TestSharedAudioKey_SeparatesEngines(t *testing.T) {
	h := contentHash("hello")
	k1 := sharedAudioKey("kokoro", h, ".mp3")
	k2 := sharedAudioKey("openai", h, ".mp3")
	if k1 == k2 {
		t.Fatal("same text on different engines must map to different keys")
	}
	if k1 != "shared/audio/kokoro/"+h+".mp3" {
		t.Fatalf("unexpected key format: %s", k1)
	}
}

func TestExpandTitleAbbreviations(t *testing.T) {
	cases := map[string]string{
		"My dear Mr. Bennet, have you heard?":     "My dear Mister Bennet, have you heard?",
		"Mrs. Long and Dr. Smith arrived.":        "Missus Long and Doctor Smith arrived.",
		"They visited St. Paul with Capt. Hook.":  "They visited Saint Paul with Captain Hook.",
		"John Smith Jr. met Prof. Jones.":         "John Smith Junior met Professor Jones.",
	}
	for in, want := range cases {
		if got := expandTitleAbbreviations(in); got != want {
			t.Errorf("expandTitleAbbreviations(%q)\n  got  %q\n  want %q", in, got, want)
		}
	}
	// Must NOT touch real sentence-ending periods or non-title words.
	keep := "He walked in. She left. The cat sat."
	if got := expandTitleAbbreviations(keep); got != keep {
		t.Errorf("altered normal sentence periods: %q", got)
	}
}
