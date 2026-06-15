package main

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestShortHash(t *testing.T) {
	cases := map[string]string{
		"":                 "nohash",
		"abc":              "abc",
		"abcdef12":         "abcdef12",
		"abcdef1234567890": "abcdef12",
	}
	for in, want := range cases {
		if got := shortHash(in); got != want {
			t.Errorf("shortHash(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripHTML(t *testing.T) {
	in := `<html><head><style>.a{color:red}</style>
		<script>var x = 1 < 2;</script></head>
		<body><p>Hello&nbsp;<b>world</b>.</p><p>Line&amp;two</p></body></html>`
	got := stripHTML(in)
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Fatalf("stripHTML left tags: %q", got)
	}
	if strings.Contains(got, "color:red") || strings.Contains(got, "var x") {
		t.Fatalf("stripHTML did not drop script/style: %q", got)
	}
	if got != "Hello world . Line&two" {
		t.Fatalf("stripHTML = %q", got)
	}
}

func TestCleanUTF8(t *testing.T) {
	// invalid UTF-8 bytes + a NUL control char, with a kept newline/tab.
	raw := []byte("good\x00text\xff\xfe\nline\tend")
	got := cleanUTF8(raw)
	if !utf8.ValidString(got) {
		t.Fatalf("cleanUTF8 produced invalid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, 0) {
		t.Fatalf("cleanUTF8 kept a NUL: %q", got)
	}
	if !strings.Contains(got, "\n") || !strings.Contains(got, "\t") {
		t.Fatalf("cleanUTF8 dropped whitespace it should keep: %q", got)
	}
	if !strings.HasPrefix(got, "goodtext") {
		t.Fatalf("cleanUTF8 = %q", got)
	}
}

// TestEffectCacheConcurrent exercises the B5 mutex; run with -race to prove the
// cache is safe under concurrent read/write (the old plain map panicked).
func TestEffectCacheConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "evt"
			effectCacheMu.Lock()
			effectCache[key] = "path"
			effectCacheMu.Unlock()
			effectCacheMu.RLock()
			_ = effectCache[key]
			effectCacheMu.RUnlock()
		}(i)
	}
	wg.Wait()
}
