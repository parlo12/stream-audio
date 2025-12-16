# Multi-Voice Narration System - Technical Guide

## Overview

The upgraded TTS system now supports **multi-voice character narration** with automatic character detection, gender/age-based voice assignment, and natural human-like speech that properly interprets punctuation instead of reading it aloud.

---

## Key Features

### ✅ **Automatic Character Detection**
- Uses GPT-4o to analyze book text and identify all speaking characters
- Detects character gender (male/female/child/neutral)
- Detects character age (adult/child/elderly)
- Assigns appropriate voices automatically

### ✅ **Multi-Voice Narration**
- Different voices for each character
- Separate narrator voice
- Natural character dialogue with appropriate voice characteristics

### ✅ **Smart Punctuation Handling**
- **Does NOT read symbols aloud**: `<` `>` `{` `}` `[` `]` `|` `\` `~` `^` `*`
- **Removes these from narration**: Quotation marks, brackets, special characters
- **Keeps natural punctuation**: `.` `,` `!` `?` `;` `:` `...` `—` (for natural pauses)
- **TTS interprets punctuation as pauses and intonation changes**

### ✅ **Enhanced Natural Narration**
- Uses **gpt-4o-mini-tts** model (latest OpenAI TTS with better steerability)
- **Instructions parameter** guides how to speak (sympathetic, expressive, natural)
- No more SSML (OpenAI TTS doesn't support it natively)
- Direct text-to-speech with smart instructions

---

## Voice Mapping

| Character Type | OpenAI Voice | Characteristics |
|----------------|--------------|-----------------|
| Male Adult | `onyx` | Deep, mature male voice |
| Female Adult | `nova` | Clear, natural female voice |
| Child (any gender) | `shimmer` | Higher-pitched, youthful voice |
| Elderly Male | `echo` | Mature, wise male voice |
| Elderly Female | `fable` | Mature, wise female voice |
| Narrator (default) | `alloy` | Neutral, warm narrator voice |

**Available Voices** (11 total):
- `alloy`, `echo`, `fable`, `onyx`, `nova`, `shimmer`
- Plus newer: `ash`, `ballad`, `coral`, `sage`, `verse`
- Latest additions: `cedar`, `marin` (most natural sounding)

---

## How It Works

### Step 1: Character Detection

When a book is uploaded, GPT-4o analyzes the first 3000 characters to identify all speaking characters:

```go
func detectCharacters(bookText string) ([]Character, error)
```

**Example Detection Result:**
```json
[
  {"name": "John", "gender": "male", "age": "adult", "voice": "onyx"},
  {"name": "Sarah", "gender": "female", "age": "adult", "voice": "nova"},
  {"name": "Timmy", "gender": "male", "age": "child", "voice": "shimmer"},
  {"name": "Narrator", "gender": "neutral", "age": "adult", "voice": "alloy"}
]
```

### Step 2: Dialogue Splitting

GPT-4o splits the text into dialogue segments, attributing each line to a speaker:

```go
func splitDialogue(bookText string, characters []Character) ([]DialogueLine, error)
```

**Example Dialogue Split:**
```json
[
  {
    "speaker": "Narrator",
    "text": "The sun rose over the hills. It was a beautiful morning.",
    "is_dialogue": false
  },
  {
    "speaker": "John",
    "text": "Good morning! How are you today?",
    "is_dialogue": true
  },
  {
    "speaker": "Narrator",
    "text": "She smiled warmly and replied.",
    "is_dialogue": false
  },
  {
    "speaker": "Sarah",
    "text": "I'm doing great, thank you. The weather is lovely.",
    "is_dialogue": true
  }
]
```

### Step 3: Text Cleaning

Before TTS, text is cleaned to remove problematic symbols:

```go
func cleanTextForNarration(text string) string
```

**Removals:**
- `< > { } [ ] | \ ~ ^ *` - Removed entirely
- `" " " ' '` - Quotation marks removed (dialogue already split)
- Other symbols that shouldn't be read aloud

**Kept for Natural Speech:**
- `. , ! ? ; :` - Period, comma, exclamation, question marks (interpreted as pauses/intonation)
- `...` - Ellipsis (natural pause)
- `—` - Em-dash (dramatic pause)

### Step 4: Voice-Specific Narration

Each dialogue line is converted to audio with character-appropriate voice and instructions:

```go
func convertTextToAudioMultiVoice(dialogueLine DialogueLine, voice string, outputPath string) error
```

**Narrator Instructions:**
```
You are an expressive audiobook narrator.
Speak naturally with appropriate pacing and emotion.
Pause briefly at commas and periods.
Use emphasis for important words.
Sound warm and engaging, like telling a story to a friend.
Do NOT read punctuation marks aloud - interpret them as natural pauses and intonation.
```

**Character Dialogue Instructions (e.g., Female Adult):**
```
You are voicing a character in an audiobook.
Speak with a natural female voice.
Speak naturally and confidently.
Express emotions naturally through your voice.
Pause at punctuation for natural speech rhythm.
Do NOT say punctuation marks like 'question mark' or 'period' - just interpret them naturally.
Sound conversational and authentic.
```

### Step 5: Audio Merging

All generated audio segments are merged into a single file using FFmpeg:

```go
func mergeAudioFiles(inputFiles []string, outputPath string) error
```

**FFmpeg Command:**
```bash
ffmpeg -f concat -safe 0 -i concat_list.txt -c copy -y output.mp3
```

---

## API Request Format

The new TTS API request uses the **instructions parameter** instead of SSML:

### Old Approach (SSML - No Longer Used):
```json
{
  "input": "<speak><prosody rate='110%'>Hello!</prosody></speak>",
  "input_format": "ssml",
  "model": "gpt-4o-mini-tts",
  "voice": "alloy"
}
```

### New Approach (Instructions-Based):
```json
{
  "input": "Hello! How are you today?",
  "model": "gpt-4o-mini-tts",
  "voice": "nova",
  "instructions": "You are voicing a cheerful female character in an audiobook. Speak with a natural female voice. Express emotions naturally through your voice. Do NOT read punctuation marks aloud - interpret them as natural pauses and intonation.",
  "response_format": "mp3",
  "speed": 1.0
}
```

---

## Code Architecture

### New Files Created:

1. **`character_detection.go`**
   - `detectCharacters()` - GPT-4o character analysis
   - `splitDialogue()` - Dialogue splitting with speaker attribution
   - `cleanTextForNarration()` - Symbol removal and text cleaning
   - `getVoiceForSpeaker()` - Voice assignment logic
   - `VoiceMapping` - Character type → OpenAI voice mapping

2. **`tts_processing_multivoice.go`**
   - `processBookConversionWithCharacters()` - Main orchestration function
   - `convertTextToAudioMultiVoice()` - Single segment TTS generation
   - `generateNarrationInstructions()` - Dynamic instruction generation
   - `mergeAudioFiles()` - FFmpeg audio concatenation
   - `copyFile()` - Utility for single-file scenarios

### Integration Point:

To enable multi-voice narration, replace the old function call:

**Old (in `fileupload.go` or `main.go`):**
```go
go processBookConversion(book)
```

**New (multi-voice enabled):**
```go
go processBookConversionWithCharacters(book)
```

---

## Configuration

### Environment Variables Required:

```bash
OPENAI_API_KEY=sk-...    # For GPT-4o character detection + TTS
XI_API_KEY=...           # For ElevenLabs background music (unchanged)
```

### System Dependencies:

- **FFmpeg** - Audio merging (already required)
- **Go 1.22+** - Language runtime

---

## Cost Considerations

### OpenAI TTS Pricing (December 2025):
- **gpt-4o-mini-tts**: $15 per 1M characters
- **Standard Quality**: Recommended for most audiobooks

### Additional API Calls:
1. **Character Detection**: ~$0.01 per book (one-time, GPT-4o)
2. **Dialogue Splitting**: ~$0.02-0.05 per book chunk (GPT-4o)
3. **TTS per segment**: $15/1M chars (same as before)

**Example Cost for 50,000 character book:**
- Old system: ~$0.75 (single voice)
- New system: ~$0.80 (multi-voice with character detection)
- **Marginal increase**: ~$0.05 per book

---

## Examples

### Input Book Text:
```
The old man sat on the porch. He looked at the sunset and sighed.

"It's beautiful, isn't it?" he asked.

The young girl nodded enthusiastically. "Yes! I love the colors! Red, orange, and pink!"

"When I was your age," he began slowly, "I used to watch sunsets with my grandfather."
```

### Character Detection Result:
```json
[
  {"name": "Old Man", "gender": "male", "age": "elderly", "voice": "echo"},
  {"name": "Young Girl", "gender": "female", "age": "child", "voice": "shimmer"},
  {"name": "Narrator", "gender": "neutral", "age": "adult", "voice": "alloy"}
]
```

### Dialogue Split Result:
```json
[
  {
    "speaker": "Narrator",
    "text": "The old man sat on the porch. He looked at the sunset and sighed.",
    "is_dialogue": false
  },
  {
    "speaker": "Old Man",
    "text": "It's beautiful, isn't it?",
    "is_dialogue": true
  },
  {
    "speaker": "Narrator",
    "text": "The young girl nodded enthusiastically.",
    "is_dialogue": false
  },
  {
    "speaker": "Young Girl",
    "text": "Yes! I love the colors! Red, orange, and pink!",
    "is_dialogue": true
  },
  {
    "speaker": "Narrator",
    "text": "When I was your age, he began slowly, I used to watch sunsets with my grandfather.",
    "is_dialogue": false
  }
]
```

### Generated Audio Files:
```
./audio/book_42_segments/segment_000.mp3  (Narrator - alloy voice)
./audio/book_42_segments/segment_001.mp3  (Old Man - echo voice)
./audio/book_42_segments/segment_002.mp3  (Narrator - alloy voice)
./audio/book_42_segments/segment_003.mp3  (Young Girl - shimmer voice)
./audio/book_42_segments/segment_004.mp3  (Narrator - alloy voice)
```

### Final Merged Audio:
```
./audio/book_42_complete.mp3  (All segments concatenated)
```

---

## Testing

### Test Case 1: Single Character Book
- **Input**: A novel with only one narrator
- **Expected**: Uses default narrator voice (alloy)
- **Behavior**: Falls back gracefully if character detection fails

### Test Case 2: Dialogue-Heavy Book
- **Input**: A book with multiple male and female characters
- **Expected**: Each character gets unique voice based on gender
- **Example**: Male character (onyx), Female character (nova), Narrator (alloy)

### Test Case 3: Children's Book
- **Input**: Book with child characters
- **Expected**: Child characters use shimmer voice (higher-pitched)
- **Adult characters**: Use age-appropriate voices

### Test Case 4: Symbol-Heavy Text
- **Input**: "She said, <thinking> 'What should I do?' {internal monologue}"
- **Cleaned**: "She said, thinking What should I do? internal monologue"
- **Expected**: No angle brackets, braces, or quotes read aloud

### Test Case 5: Punctuation Interpretation
- **Input**: "Wait... what? No! Are you serious?"
- **Expected**: Natural pauses at ellipsis, question mark creates questioning intonation, exclamation adds emphasis
- **NOT Expected**: "Wait dot dot dot what question mark No exclamation mark"

---

## Troubleshooting

### Issue: All characters sound the same
**Cause**: Voice mapping not working
**Fix**: Check `getVoiceForSpeaker()` function and character detection results
**Debug**: Add logging to see which voice is assigned to each speaker

### Issue: Symbols are being read aloud
**Cause**: `cleanTextForNarration()` not being called
**Fix**: Ensure dialogue splitting calls the cleaning function
**Verify**: Check logs for "cleaned" text before TTS

### Issue: Character detection fails
**Cause**: Book text too short or unusual format
**Fix**: System falls back to single narrator voice (graceful degradation)
**Expected**: Logs show "Using default narrator" message

### Issue: Audio merge fails
**Cause**: FFmpeg not installed or path issues
**Fix**: Install FFmpeg and ensure it's in system PATH
```bash
# macOS
brew install ffmpeg

# Ubuntu
apt-get install ffmpeg

# Verify
ffmpeg -version
```

### Issue: Too many API calls / high cost
**Cause**: Character detection running on every chunk
**Solution**: Cache character detection results per book (hash-based)
**Optimization**: Only run detection once per unique content_hash

---

## Performance Optimization

### 1. Character Detection Caching
**Current**: Runs on every book upload
**Optimized**: Cache by content_hash
```go
// Check cache before running detection
var cachedCharacters []Character
cacheKey := fmt.Sprintf("characters_%s", book.ContentHash)
// Implement Redis or in-memory cache
```

### 2. Parallel Audio Generation
**Current**: Sequential TTS calls
**Optimized**: Parallel goroutines with rate limiting
```go
// Process multiple segments simultaneously
sem := make(chan struct{}, 5) // Limit to 5 concurrent API calls
var wg sync.WaitGroup
for i, line := range dialogueLines {
    wg.Add(1)
    go func(index int, dl DialogueLine) {
        defer wg.Done()
        sem <- struct{}{} // Acquire
        defer func() { <-sem }() // Release
        convertTextToAudioMultiVoice(dl, voice, path)
    }(i, line)
}
wg.Wait()
```

### 3. Batch Dialogue Splitting
**Current**: One GPT call per book
**Optimized**: Already optimal (single call processes entire book)

---

## Migration from Old System

### Option 1: Feature Flag (Recommended)
```go
// In main.go or fileupload.go
useMultiVoice := os.Getenv("ENABLE_MULTIVOICE") == "true"

if useMultiVoice {
    go processBookConversionWithCharacters(book)
} else {
    go processBookConversion(book) // Old single-voice system
}
```

### Option 2: Gradual Rollout
- Keep both systems running
- Route new books to multi-voice
- Re-process old books on demand

### Option 3: Complete Replacement
- Remove old `tts_processing.go` functions
- Replace all calls with `processBookConversionWithCharacters`

---

## Future Enhancements

### Planned Features:
1. **Voice Cloning**: Use ElevenLabs for custom character voices
2. **Emotion Detection**: Vary voice tone based on scene emotion (happy/sad/tense)
3. **Accent Support**: Different accents for different characters (British, Southern, etc.)
4. **Voice Consistency**: Ensure same character always gets same voice across books
5. **Performance Mode**: Faster processing with lower-quality voices for previews

### Research Areas:
- **Streaming TTS**: Real-time narration as user reads
- **Voice Mixing**: Blend voices for smoother transitions
- **Custom Instructions**: User-specified voice characteristics per character

---

## Sources

- [Introducing next-generation audio models | OpenAI](https://openai.com/index/introducing-our-next-generation-audio-models/)
- [Text to speech API | OpenAI Documentation](https://platform.openai.com/docs/guides/text-to-speech)
- [GPT-4o-Mini-TTS: Steerable Speech via Simple APIs](https://blog.promptlayer.com/gpt-4o-mini-tts-steerable-low-cost-speech-via-simple-apis/)
- [Audio and Speech | OpenAI API](https://platform.openai.com/docs/guides/audio)

---

## Summary

The new multi-voice narration system provides:

✅ **Automatic character detection** with GPT-4o
✅ **Gender and age-based voice assignment**
✅ **Smart punctuation handling** (no more reading symbols aloud)
✅ **Natural human-like narration** with instructions parameter
✅ **Scalable architecture** with caching and optimization options
✅ **Cost-effective** (minimal increase over single-voice system)
✅ **Production-ready** with graceful fallbacks

Deploy with confidence! 🎙️🎭
