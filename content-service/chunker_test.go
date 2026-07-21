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

func TestWordSafeChunks_BreaksAtSentences(t *testing.T) {
	// Long enough to force a break; ~1050 runes with sentence ends throughout,
	// and a "Mr. Bennet" that must NOT be treated as a sentence boundary.
	para := "Mr. Bennet was among the earliest of those who waited on him. " +
		"He had always intended to visit, though to the last assuring his wife he should not go. "
	text := strings.Repeat(para, 10) // ~1400 runes
	runes := []rune(text)
	spans := wordSafeChunks(runes, 1000)
	if len(spans) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(spans))
	}
	// Every non-final chunk should end at a sentence terminator (after trimming).
	for i := 0; i < len(spans)-1; i++ {
		c := strings.TrimRight(string(runes[spans[i][0]:spans[i][1]]), " \t\n\"'”’)]")
		last := c[len(c)-1]
		if last != '.' && last != '!' && last != '?' {
			t.Fatalf("chunk %d did not end at a sentence: ...%q", i, c[max0(len(c)-30):])
		}
		// and it must not end on "Mr" (abbreviation split)
		if strings.HasSuffix(c, "Mr.") {
			t.Fatalf("chunk %d broke after an abbreviation 'Mr.'", i)
		}
	}
}

func max0(x int) int { if x < 0 { return 0 }; return x }

func TestIsSentenceEndAt(t *testing.T) {
	check := func(s string, pos int, want bool) {
		r := []rune(s)
		if got := isSentenceEndAt(r, pos, len(r)); got != want {
			t.Errorf("isSentenceEndAt(%q @%d)=%v want %v", s, pos, got, want)
		}
	}
	check("He left. She stayed.", 7, true)     // '.' after "left" before " She"
	check("Mr. Bennet arrived.", 2, false)     // abbreviation period
	check("It cost 3.14 dollars.", 9, false)   // decimal
	check("Who is there? He asked.", 12, true) // '?' sentence end
	check("See J. Smith today.", 5, false)     // single-letter initial
}

func TestCleanupForTTS_FluencyNormalization(t *testing.T) {
	// OCR mid-sentence newline must become a space (no Kokoro pause), and
	// space-before-punctuation must be tidied.
	in := "A single man of \nlarge fortune ; four or five thousand a year . What a fine\nthing for our girls !"
	got := cleanupForTTS(in)
	if strings.Contains(got, "\n") {
		t.Fatalf("newlines survived: %q", got)
	}
	for _, bad := range []string{" ;", " .", " !", "of \nlarge", "of  large"} {
		if strings.Contains(got, bad) {
			t.Fatalf("artifact %q survived: %q", bad, got)
		}
	}
	if !strings.Contains(got, "man of large fortune;") || !strings.Contains(got, "girls!") {
		t.Fatalf("expected clean flowing text, got: %q", got)
	}
}
