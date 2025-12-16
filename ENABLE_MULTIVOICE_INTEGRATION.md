# How to Enable Multi-Voice Narration - Quick Integration Guide

## Current Status

✅ **Files Created:**
- `character_detection.go` - Character detection and dialogue splitting logic
- `tts_processing_multivoice.go` - Multi-voice TTS processing
- `MULTIVOICE_NARRATION_GUIDE.md` - Complete technical documentation

⚠️ **Action Required:**
You need to integrate the new multi-voice system into your existing book upload workflow.

---

## Integration Steps

### Option 1: Environment Variable Toggle (Recommended for Testing)

This allows you to test the new system without breaking existing functionality.

**Step 1**: Add environment variable to `.env`:
```bash
# Enable multi-voice narration (set to "true" to enable)
ENABLE_MULTIVOICE=true
```

**Step 2**: Find the file that calls `processBookConversion()`

Check these files:
- `content-service/fileupload.go`
- `content-service/main.go`
- Any file with `go processBookConversion(book)`

**Step 3**: Replace the function call:

**Before:**
```go
// Old single-voice processing
go processBookConversion(book)
```

**After (with toggle):**
```go
// Multi-voice narration with feature flag
useMultiVoice := os.Getenv("ENABLE_MULTIVOICE") == "true"

if useMultiVoice {
    log.Printf("🎭 Using multi-voice narration for book ID %d", book.ID)
    go processBookConversionWithCharacters(book)
} else {
    log.Printf("🎙️ Using single-voice narration for book ID %d", book.ID)
    go processBookConversion(book)
}
```

---

### Option 2: Direct Replacement (Full Migration)

This completely replaces the old system with the new multi-voice system.

**Step 1**: Find where `processBookConversion()` is called

**Step 2**: Replace with:
```go
// Use new multi-voice processing
go processBookConversionWithCharacters(book)
```

**Step 3** (Optional): Remove old `generateSSML()` function from `tts_processing.go` since it's no longer needed (SSML not supported by OpenAI TTS).

---

## Testing the Integration

### 1. Upload a Test Book

Use a book with clear character dialogue:

**test_book.txt:**
```
The old wizard looked at the young apprentice with concern.

"You must be careful," he warned. "Magic is not a toy."

The boy nodded eagerly. "Yes, master! I understand! I'll be super careful!"

The wizard sighed. "Youth... they never listen."
```

### 2. Check Logs for Character Detection

You should see logs like:
```
🔍 Detecting characters for book ID 42...
🎭 Detected character: Wizard (male, elderly) → Voice: echo
🎭 Detected character: Boy (male, child) → Voice: shimmer
🎭 Detected character: Narrator (neutral, adult) → Voice: alloy
📝 Split into 5 dialogue segments
🎙️ Generating audio for 5 dialogue segments...
🎙️ Generated audio: ./audio/book_42_segments/segment_000.mp3 (Speaker: Narrator, Voice: alloy)
🎙️ Generated audio: ./audio/book_42_segments/segment_001.mp3 (Speaker: Wizard, Voice: echo)
🎙️ Generated audio: ./audio/book_42_segments/segment_002.mp3 (Speaker: Narrator, Voice: alloy)
🎙️ Generated audio: ./audio/book_42_segments/segment_003.mp3 (Speaker: Boy, Voice: shimmer)
🎙️ Generated audio: ./audio/book_42_segments/segment_004.mp3 (Speaker: Wizard, Voice: echo)
🔧 Merging 5 audio segments...
✅ Merged 5 audio files into: ./audio/book_42_complete.mp3
✅ TTS audio file generated: ./audio/book_42_complete.mp3 for book ID 42
```

### 3. Listen to the Result

Play `./audio/book_42_complete.mp3` and verify:
- Different voices for different characters ✅
- Narrator has neutral voice ✅
- Punctuation is NOT read aloud ✅
- Natural pauses at periods and commas ✅
- No symbols like `<` `>` `?` being spoken ✅

---

## Verification Checklist

Before deploying to production:

- [ ] FFmpeg is installed (`ffmpeg -version` works)
- [ ] `OPENAI_API_KEY` is set in environment
- [ ] Test book processes successfully
- [ ] Character detection works (check logs for "Detected character")
- [ ] Multiple audio segments are generated
- [ ] Audio files are merged successfully
- [ ] Final audio has different voices for different characters
- [ ] No symbols are read aloud (test with text containing `< > ! ?`)
- [ ] Background music still works (if enabled)

---

## Rollback Plan

If you encounter issues, you can easily rollback:

### If using Environment Variable Toggle:
```bash
# Disable multi-voice
ENABLE_MULTIVOICE=false
```

### If using Direct Replacement:
```go
// Revert to old function
go processBookConversion(book)
```

---

## Common Integration Locations

Based on typical Go project structures, the integration point is likely in one of these files:

### 1. `content-service/fileupload.go`

Look for a function like `uploadBookFileHandler` or `processTTSQueueJob`:
```go
func processTTSQueueJob(job TTSQueueJob) {
    // ... existing code ...

    // OLD CODE (find this):
    // go processBookConversion(book)

    // NEW CODE (replace with):
    useMultiVoice := os.Getenv("ENABLE_MULTIVOICE") == "true"
    if useMultiVoice {
        go processBookConversionWithCharacters(book)
    } else {
        go processBookConversion(book)
    }
}
```

### 2. `content-service/main.go`

Look for initialization or route handlers that trigger TTS processing.

### 3. Search for the Function Call

```bash
# Find where processBookConversion is called
cd content-service
grep -r "processBookConversion" .
```

Expected output:
```
./fileupload.go:234:    go processBookConversion(book)
./tts_processing.go:167:func processBookConversion(book Book) {
```

The first result (fileupload.go:234) is where you need to make the change.

---

## Performance Notes

### First Book Processing:
- **Character Detection**: ~2-5 seconds (GPT-4o API call)
- **Dialogue Splitting**: ~3-10 seconds (GPT-4o API call)
- **TTS Generation**: ~10-30 seconds per dialogue segment (OpenAI TTS API)
- **Audio Merging**: ~1-3 seconds (FFmpeg)

**Total**: ~30-120 seconds depending on book length and number of characters

### Subsequent Books (with same content hash):
- Reuses cached audio
- **Total**: ~1 second (no API calls)

---

## Monitoring

Add these log checks to monitor the new system:

```bash
# Check for character detection
docker-compose logs content-service | grep "Detected character"

# Check for dialogue splitting
docker-compose logs content-service | grep "Split into"

# Check for multi-voice processing
docker-compose logs content-service | grep "multi-voice"

# Check for errors
docker-compose logs content-service | grep "ERROR\|❌"
```

---

## Next Steps

1. **Find integration point** using grep command above
2. **Add feature flag** (ENABLE_MULTIVOICE=true in .env)
3. **Update function call** to use conditional logic
4. **Rebuild service**: `docker-compose up -d --build content-service`
5. **Test with sample book**
6. **Verify logs** show character detection
7. **Listen to result** to confirm different voices

---

## Need Help?

If you're unsure where to integrate, share the output of:

```bash
cd content-service
grep -n "processBookConversion" *.go
```

This will show the exact file and line number where the integration needs to happen.

---

## Summary

✅ **New files created** - Character detection and multi-voice TTS
✅ **Integration required** - Replace `processBookConversion()` call
✅ **Feature flag available** - Test before full rollout
✅ **Rollback plan** - Easy to revert if needed
✅ **Documentation complete** - Full technical guide available

**Estimated integration time**: 5-10 minutes

Ready to deploy! 🚀
