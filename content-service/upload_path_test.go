package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUploadDirForBook_AlwaysUnderBase proves the upload destination is derived
// purely from numeric IDs and always stays under ./uploads (S7 — no traversal).
func TestUploadDirForBook_AlwaysUnderBase(t *testing.T) {
	base, _ := filepath.Abs(uploadBaseDir)
	cases := []struct{ user, book uint }{
		{1, 1}, {42, 1000}, {999999, 7},
	}
	for _, tc := range cases {
		got, _ := filepath.Abs(uploadDirForBook(tc.user, tc.book))
		if !strings.HasPrefix(got, base+string(filepath.Separator)) {
			t.Fatalf("uploadDirForBook(%d,%d)=%q escaped base %q", tc.user, tc.book, got, base)
		}
		// The final path component must be the book id, the parent the user id.
		if filepath.Base(got) != itoa(tc.book) || filepath.Base(filepath.Dir(got)) != itoa(tc.user) {
			t.Fatalf("unexpected layout for (%d,%d): %q", tc.user, tc.book, got)
		}
	}
}

// TestValidUploadExt ignores everything but the allow-listed extension and never
// returns anything derived from a malicious path.
func TestValidUploadExt(t *testing.T) {
	cases := map[string]string{
		"book.pdf":              ".pdf",
		"My Book.EPUB":          ".epub",
		"novel.AZW3":            ".azw3",
		"weird.azw":             ".azw",
		"../../etc/passwd.pdf":  ".pdf", // traversal in name → still just the ext
		"/tmp/../x/story.txt":   ".txt",
		"malware.kfx":           "",     // unsupported
		"noext":                 "",
		"trick.pdf.exe":         "",     // not a supported suffix
	}
	for name, want := range cases {
		if got := validUploadExt(name); got != want {
			t.Errorf("validUploadExt(%q) = %q, want %q", name, got, want)
		}
	}
}

// itoa is a tiny test helper to avoid importing strconv just for formatting.
func itoa(u uint) string {
	if u == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}
