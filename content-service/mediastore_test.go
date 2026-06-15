package main

import "testing"

func TestKeyBuilders(t *testing.T) {
	if got := audioPageKey(7, 3, "abcdef1234567890", ".mp3"); got != "audio/7/page_3_abcdef12.mp3" {
		t.Errorf("audioPageKey = %q", got)
	}
	if got := groupAudioKey(7, 0, 19); got != "audio/7/chunks_0_19.mp3" {
		t.Errorf("groupAudioKey = %q", got)
	}
	if got := bookAudioKey(7); got != "audio/7/book.mp3" {
		t.Errorf("bookAudioKey = %q", got)
	}
	if got := coverKey(7, "seed12345", ".jpg"); got != "covers/7/seed1234.jpg" {
		t.Errorf("coverKey = %q", got)
	}
	if got := uploadKey(42, 7, ".pdf"); got != "uploads/42/7/original.pdf" {
		t.Errorf("uploadKey = %q", got)
	}
	if got := legacyKey("./audio/book_5_page_2_ab.mp3", "audio"); got != "legacy/audio/book_5_page_2_ab.mp3" {
		t.Errorf("legacyKey = %q", got)
	}
}

func TestIsLegacyLocalPath(t *testing.T) {
	legacy := []string{"./audio/x.mp3", "/app/audio/x.mp3", "/opt/stream-audio-data/audio/x.mp3"}
	keys := []string{"audio/7/page_1_ab.mp3", "covers/7/h.jpg", "uploads/1/2/original.pdf", "legacy/audio/x.mp3"}
	for _, p := range legacy {
		if !isLegacyLocalPath(p) {
			t.Errorf("expected legacy: %q", p)
		}
	}
	for _, k := range keys {
		if isLegacyLocalPath(k) {
			t.Errorf("expected key (not legacy): %q", k)
		}
	}
}

func TestContentTypeForExt(t *testing.T) {
	cases := map[string]string{
		"x.mp3": "audio/mpeg", "x.ogg": "audio/ogg", "x.jpg": "image/jpeg",
		"x.png": "image/png", "x.pdf": "application/pdf", "x.bin": "application/octet-stream",
	}
	for in, want := range cases {
		if got := contentTypeForExt(in); got != want {
			t.Errorf("contentTypeForExt(%q) = %q, want %q", in, got, want)
		}
	}
}
