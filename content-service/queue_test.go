package main

import (
	"encoding/json"
	"testing"
)

func TestTranscribeBatchPayloadRoundTrip(t *testing.T) {
	in := TaskTranscribeBatch{BookID: 7, StartPage: 0, EndPage: 19, UserID: 42, AccountType: "free"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out TaskTranscribeBatch
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: %+v != %+v", out, in)
	}
}

func TestBatchSizeConstant(t *testing.T) {
	// The first batch covers pages [start, start+batchSizePages-1] = 20 pages.
	if batchSizePages != 20 {
		t.Fatalf("batchSizePages = %d, want 20", batchSizePages)
	}
	start := 0
	end := start + batchSizePages - 1
	if end != 19 {
		t.Fatalf("first batch end = %d, want 19", end)
	}
}

func TestTaskTypeNames(t *testing.T) {
	for _, ty := range []string{TypeTranscribeBatch, TypeMergeChunks, TypeFetchCover} {
		if ty == "" {
			t.Fatal("empty task type constant")
		}
	}
}
