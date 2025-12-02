# Stream Audio Project

## Project Overview

**Stream Audio** is a sophisticated microservices-based audiobook and streaming platform built with Go. It converts text documents (PDF, TXT, EPUB, MOBI, AZW, AZW3) into high-quality audio with AI-powered text-to-speech (TTS), dynamic background music generation, and Foley sound effects. The system includes user authentication, subscription management via Stripe, and real-time content processing.

## Architecture

### Microservices Structure

```
stream-audio/
├── gateway/              # API Gateway (Port 8080) - Entry point for all requests
├── auth-service/         # Authentication & User Management (Port 8082)
├── content-service/      # Book Processing & Audio Generation (Port 8083)
├── docker-compose.yml    # Local development setup
├── docker-compose.prod.yml # Production deployment
└── .env.example         # Environment configuration template
```

### Service Responsibilities

#### 1. Gateway Service (Port 8080)
**Location**: [gateway/main.go](gateway/main.go)

- Reverse proxy router serving as single entry point
- Routes `/signup`, `/login`, `/auth/*` → Auth service (8082)
- Routes `/content/*` → Content service (8083)
- Health check endpoint at `/health`

**Technology**: Gin framework, Go 1.22

#### 2. Auth Service (Port 8082)
**Location**: [auth-service/main.go](auth-service/main.go)

**Responsibilities**:
- User signup/login with JWT token generation (72 hour expiry)
- Password hashing with bcrypt
- Stripe payment integration for subscriptions
- Account type management (free/paid)
- Webhook handling for Stripe events

**Key API Endpoints**:
- `POST /signup` - Register new user
- `POST /login` - Authenticate user, return JWT
- `GET /user/profile` - Get user profile (protected)
- `POST /user/stripe/create-checkout-session` - Create Stripe checkout
- `GET /user/account-type` - Check account tier
- `POST /stripe/webhook` - Handle Stripe events

**Data Model**:
```go
User {
    ID, Username, Email (unique)
    Password (bcrypt hashed)
    AccountType (free/paid)
    IsPublic, State
    StripeCustomerID
    BooksRead count
    CreatedAt, UpdatedAt
}
```

**Technology**: Gin, GORM, PostgreSQL, JWT, bcrypt, Stripe API v78

#### 3. Content Service (Port 8083)
**Location**: [content-service/main.go](content-service/main.go)

**Responsibilities**:
- Book creation, uploading, and management
- Document chunking (breaks books into ~1000 character pages)
- Text extraction from PDF, TXT, EPUB, MOBI, AZW, and AZW3 files
- Text-to-speech (TTS) audio generation via OpenAI API
- Background music generation via ElevenLabs API
- Dynamic audio mixing and Foley effect overlays
- Audio streaming with authentication
- Book cover image upload and management
- MQTT event publishing for real-time updates

**Core Components**:

1. **[fileupload.go](content-service/fileupload.go)** - Upload and chunk documents
   - `uploadBookFileHandler` - Handle file uploads (PDF/TXT/EPUB/MOBI/AZW/AZW3)
   - `ChunkDocument` - Split text into ~1000 char chunks
   - `computeFileHash` - SHA256 hashing for deduplication
   - Supports formats: PDF, TXT, EPUB, MOBI, AZW, AZW3
   - KFX format explicitly rejected with helpful error message

2. **[document_chunker.go](content-service/document_chunker.go)** - Text extraction
   - `ExtractTextFromPDF` - Parse PDF pages
   - `ExtractTextFromTXT` - Read plain text
   - `ExtractTextFromEPUB` - Extract from EPUB (ZIP)
   - `ExtractTextFromMOBI` - Convert MOBI/AZW/AZW3 to text using Calibre's ebook-convert
     - Checks for Calibre installation
     - Creates temporary TXT file for conversion
     - Cleans up temp files automatically
     - Returns helpful error if Calibre is missing

3. **[tts_processing.go](content-service/tts_processing.go)** - Text-to-speech conversion
   - `convertTextToAudio` - Call OpenAI TTS API (gpt-4o-mini-tts)
   - `generateSSML` - Convert text to SSML with prosody/emphasis
   - `processBookConversion` - Orchestrate TTS workflow
   - Caches audio files by content hash to avoid duplication

4. **[sound_effects.go](content-service/sound_effects.go)** - Background music & Foley
   - `generateSoundEffect` - Call ElevenLabs API for 22s audio clips
   - `generateSegmentInstructions` - Use GPT-4 to analyze mood segments
   - `generateDynamicBackgroundWithSegments` - FFmpeg dynamic audio mixing
   - `extractSoundEvents` - GPT identifies sword_clash, door_creak, etc.
   - `overlaySoundEvents` - Mix Foley effects with main audio
   - Volume balancing: TTS (1.0), background (0.3), effects (0.45)

5. **[chunk_merger.go](content-service/chunk_merger.go)** - Merge multiple chunks
   - `processMergedChunks` - Combine chunks into single audio file
   - Uses FFmpeg concat protocol

6. **[mqtt.go](content-service/mqtt.go)** - Real-time event publishing
   - `InitMQTT` - Connect to MQTT broker (supports TLS, auth)
   - `PublishEvent` - Publish JSON payloads to topics
   - Non-blocking initialization with 5s timeout

7. **[streaming.go](content-service/streaming.go)** - Audio streaming
   - `proxyBookAudioHandler` - Stream audio with token verification
   - JWT validation in query param or Authorization header
   - Access control: users can only stream their own books

8. **[bookCoverUpload.go](content-service/bookCoverUpload.go)** - Book covers (Legacy manual upload)
   - `uploadBookCoverHandler` - Accept JPG/JPEG/PNG
   - Async DB update + MQTT publication

9. **[bookCoverWebSearch.go](content-service/bookCoverWebSearch.go)** - Automatic book cover fetching
   - `fetchBookCoverFromWeb` - Query web for book covers using OpenAI Responses API
   - `downloadAndSaveImage` - Download and save cover images locally
   - `fetchAndSaveBookCover` - Main entry point for automatic cover fetching
   - Integrated into book creation workflow
   - Uses OpenAI's web search tool to find official book covers
   - Validates image dimensions (target: 1000×1600px, aspect ratio 0.625)

**Key API Endpoints**:
```
Book Management:
- POST /user/books - Create book
- GET /user/books - List user's books (filterable by category/genre)
- GET /user/books/:book_id - Get single book
- DELETE /user/books/:book_id - Delete book
- POST /user/books/upload - Upload document
- GET /user/books/:book_id/chunks/pages - List pages with pagination

Cover Management:
- POST /user/books/:book_id/cover - Upload book cover (legacy manual upload)
- Automatic cover fetching on book creation via OpenAI web search

TTS Processing:
- POST /user/chunks/tts - Process chunks to audio
- POST /user/books/:book_id/tts/batch - Batch transcribe by page
- GET /user/chunks/tts/merged-audio/:book_id - Stream merged audio
- GET /user/books/:book_id/pages/:page/audio - Stream single page audio

Audio Streaming:
- GET /user/books/stream/proxy/:book_id - Stream book audio
- GET /user/books/:book_id/pages/:page/audio - Stream page audio
- POST /user/chunks/audio-by-id - Stream audio by chunk IDs
- GET /user/books/:book_id/chunks/:start/:end/audio - Stream chunk range
```

**Data Models**:
```go
Book {
    ID, Title, Author, Category, Genre
    Content, ContentHash (SHA256)
    FilePath, AudioPath, CoverPath, CoverURL
    Status (pending, processing, completed, etc.)
    UserID (owner), Index
}

BookChunk {
    ID, BookID, Index
    Content (text segment)
    AudioPath, FinalAudioPath
    TTSStatus (pending, processing, completed, failed)
    StartTime, EndTime
}

ProcessedChunkGroup {
    ID, BookID, StartIndex, EndIndex
    AudioPath, CreatedAt
}

TTSQueueJob {
    ID, BookID, UserID
    ChunkIDs (comma-separated)
    Status (queued, processing, complete, failed)
}
```

**Technology**: Gin, GORM, PostgreSQL, FFmpeg, OpenAI API, ElevenLabs API, MQTT, JWT, Paho MQTT Go client

## Technology Stack

| Component | Technology | Version |
|-----------|-----------|---------|
| Language | Go | 1.22-1.24.2 |
| Web Framework | Gin | v1.10.0 |
| Database ORM | GORM | v1.25.12 |
| Database | PostgreSQL | External/DigitalOcean |
| Authentication | JWT | v3.2.2 |
| Password Hashing | bcrypt | golang.org/x/crypto |
| MQTT Client | Paho MQTT | v1.5.0 |
| TTS Engine | OpenAI API | gpt-4o-mini-tts |
| Sound Effects | ElevenLabs API | v1 |
| Payment | Stripe | v78.12.0 |
| Audio Processing | FFmpeg | System binary |
| PDF Parsing | rsc.io/pdf | v0.1.1 |
| EPUB Parsing | Archive/zip | Go stdlib |
| MOBI/AZW Parsing | Calibre (ebook-convert) | System binary |
| Caching | Redis | 7-alpine |
| Container | Docker | Multi-stage builds |

## Database Schema

**Tables** (PostgreSQL):

1. **users** (Auth Service)
   - Columns: id, username, email, password, account_type, is_public, state, stripe_customer_id, books_read
   - Indexes: email (unique), username (unique)

2. **books** (Content Service)
   - Columns: id, title, author, content, content_hash, file_path, audio_path, cover_path, cover_url, status, category, genre, user_id, index
   - Indexes: content_hash, category, genre, user_id

3. **book_chunks** (Content Service)
   - Columns: id, book_id, index, content, audio_path, final_audio_path, tts_status, start_time, end_time
   - Indexes: book_id

4. **processed_chunk_groups** (Content Service)
   - Columns: id, book_id, start_index, end_index, audio_path
   - Indexes: book_id

5. **tts_queue_jobs** (Content Service)
   - Columns: id, book_id, chunk_ids, status, user_id
   - Indexes: book_id, user_id

## Key Features

### 1. Document Processing Pipeline

```
Upload (PDF/TXT/EPUB/MOBI/AZW/AZW3)
  ↓
Hash Calculation (SHA256)
  ↓
Text Extraction (format-specific)
  ↓
Chunking (~1000 chars per chunk)
  ↓
Database Storage (BookChunk records)
  ↓
Optional: Batch TTS Transcription
```

**Supported Formats**:
- **PDF** - Parsed using rsc.io/pdf library
- **TXT** - Plain text files
- **EPUB** - Extracted from ZIP archive (XHTML/HTML files)
- **MOBI** - Kindle format converted using Calibre's ebook-convert tool
- **AZW** - Amazon Kindle format (MOBI-based), converted using Calibre
- **AZW3** - Amazon Kindle format (KF8/MOBI-based), converted using Calibre

**Technical Implementation for MOBI/AZW Formats**:
- Uses Calibre's `ebook-convert` command-line tool to convert MOBI/AZW/AZW3 to TXT
- Temporary files are created in the system temp directory and cleaned up automatically
- UTF-8 encoding is enforced for proper text extraction
- If Calibre is not installed, users receive a clear error message with installation instructions

**Unsupported Format**:
- **KFX** - Amazon's proprietary Kindle Format 10 is not supported due to lack of available Go libraries. Users will receive a helpful error message suggesting conversion to supported formats using Calibre or other tools.

### 2. Text-to-Speech (TTS) Workflow

```
1. Generate SSML from plain text via GPT-4
   - Add breaks, emphasis, prosody rate adjustments

2. Convert SSML to MP3 via OpenAI API
   - Model: gpt-4o-mini-tts
   - Voice: alloy
   - Response Format: mp3

3. Cache result by content hash
   - Reuse audio for duplicate content
   - Status: "TTS completed", "TTS reused"
```

### 3. Dynamic Background Music Generation

```
1. Analyze book excerpt with GPT-4
   - Generate concise music prompt (max 300 chars)
   - Consider instrumentation, mood, style

2. Generate 22-second base music via ElevenLabs
   - Sound effect generation API
   - PromptInfluence: 0.5

3. Segment TTS duration using GPT-4
   - Identify mood changes (suspense, action, climax, sad, neutral)
   - Create time-based segments

4. Dynamically stretch/repeat background clip
   - Use FFmpeg adelay to delay segments
   - Volume adjustment (0.30)
   - Trim to exact TTS duration

5. Mix TTS + Background
   - TTS volume: 1.0
   - Background volume: 0.3
   - Output format: MP3 (libmp3lame, quality 2)
```

### 4. Foley Sound Effects Overlay

```
1. Extract sound events from book text via GPT
   - Event types: sword_clash, door_creak, thunder, etc.
   - Map to timestamps

2. Generate/cache effect clips via ElevenLabs
   - Pre-defined prompts for common events

3. Overlay effects using FFmpeg
   - Use adelay filter for timing
   - Volume: 0.45
   - Output format: Opus (64kbps)
```

### 5. Content Deduplication

- SHA256 hashing of file content
- Audio reuse when identical content detected
- Reduces API calls to OpenAI/ElevenLabs
- Status tracking: "TTS completed" vs "TTS reused"

### 6. Streaming & Access Control

- JWT validation (header or query param for iOS AVPlayer)
- Access control: users can only stream their own books
- Support for:
  - Single page audio
  - Merged chunk groups
  - Arbitrary chunk ranges
  - Entire book audio

### 7. Real-Time Event Publishing

- MQTT broker integration for live updates
- Topics: `users/{userId}/cover_uploaded`, `debug/ping`
- Non-blocking connection with 5s timeout
- Support for TLS and username/password auth

### 8. Subscription Management

- Free account limits: 1 completed chunk max
- Paid account: Unlimited processing
- Stripe webhook integration:
  - `checkout.session.completed` → Upgrade to "paid"
  - `customer.subscription.deleted` → Downgrade to "free"

## Configuration

### Required Environment Variables

```bash
# Database
DB_HOST=postgres
DB_USER=rolf
DB_PASSWORD=<set in deploy>
DB_NAME=streaming_db
DB_PORT=5432
DB_SSLMODE=disable (local) | require (prod)

# Authentication
JWT_SECRET=<set in deploy>
REDIS_URL=redis://redis:6379

# APIs
OPENAI_API_KEY=<set in deploy>           # For TTS & GPT prompts
XI_API_KEY=<set in deploy>               # For ElevenLabs sound effects

# Payment
STRIPE_SECRET_KEY=<set in deploy>
STRIPE_WEBHOOK_SECRET=<set in deploy>

# MQTT
MQTT_BROKER=tcp://mqtt-broker:1883       # or tls://...
MQTT_USERNAME=<optional>
MQTT_PASSWORD=<optional>

# Deployment
GIN_MODE=release (prod) | debug (dev)
STREAM_HOST=http://localhost:8083        # Public URL for streaming
```

## Deployment

### Local Development (macOS/Windows)

For local development, use the dedicated local compose file which includes PostgreSQL and uses Docker-managed volumes:

```bash
# First time: Build all images
docker compose -f docker-compose.local.yml build

# Start all services
docker compose -f docker-compose.local.yml up -d

# View logs
docker compose -f docker-compose.local.yml logs -f

# Stop services
docker compose -f docker-compose.local.yml down
```

**Services available at:**
- PostgreSQL: `localhost:5433`
- Redis: `localhost:6380`
- Auth Service: `localhost:8082`
- Content Service: `localhost:8083`

**Note:** Local development uses:
- Internal PostgreSQL container (not external database)
- Docker-managed volumes (no `/opt/` bind mounts required)
- Debug mode for easier debugging
- SSL disabled for local connections

### Production Deployment (Linux Server)

For production on Linux servers with external PostgreSQL database:

```bash
# First, ensure /opt/stream-audio-data/ directories exist
sudo mkdir -p /opt/stream-audio-data/{audio,covers,uploads,redis}
sudo chown -R $USER:$USER /opt/stream-audio-data

# Deploy with production configuration
docker compose -f docker-compose.prod.yml up -d --build --force-recreate

# Check status
docker compose -f docker-compose.prod.yml ps

# View logs
docker compose -f docker-compose.prod.yml logs -f content-service
```

**Production configuration includes:**
- External PostgreSQL (DigitalOcean or other)
- Persistent volumes at `/opt/stream-audio-data/`
- Healthchecks enabled
- JSON logging with rotation (10MB max, 3 files)
- SSL/TLS enabled for database connections
- Release mode (optimized performance)

### Docker Architecture

Each service uses a two-stage build:
1. **Build Stage**: Golang:1.22/1.23 - Compile Go code with `-ldflags="-s -w"`
2. **Runtime Stage**: Lightweight base image (distroless/alpine/debian-slim)

### System Requirements

The **content-service** Docker container includes the following system dependencies:
- **FFmpeg** - Audio processing and manipulation
- **PostgreSQL client** - Database connectivity
- **Calibre** - MOBI/AZW/AZW3 ebook format conversion via `ebook-convert` command
- **CA certificates** - SSL/TLS support

For local development outside Docker, ensure these tools are installed:
```bash
# macOS
brew install ffmpeg calibre postgresql

# Ubuntu/Debian
apt-get install ffmpeg calibre postgresql-client

# The content-service will check for ebook-convert at runtime
# and provide clear error messages if Calibre is missing
```

### Persistent Volumes

- `content-audio-persistent` → `/opt/stream-audio-data/audio`
- `content-covers-persistent` → `/opt/stream-audio-data/covers`
- `content-uploads` → `/opt/stream-audio-data/uploads`
- `redis_data` → `/opt/stream-audio-data/redis`

## API Workflows

### Example 1: Upload & Process Book

```bash
# 1. Create user account
POST /signup → JWT token

# 2. Create book record
POST /user/books → book_id
  - Book cover automatically fetched in background
  - Uses OpenAI web search to find official cover
  - Downloads and saves image locally
  - Publishes MQTT event when cover is ready

# 3. Upload document (PDF/TXT/EPUB/MOBI/AZW/AZW3)
POST /user/books/upload
  - File is chunked automatically
  - Returns total_pages count
  - Validates file format
  - Rejects KFX with helpful conversion message

# 4. Start batch transcription
POST /user/books/{id}/tts/batch
  - Each chunk: text→SSML→MP3→music→effects
  - Async processing

# 5. Check progress
GET /user/books/{id}/chunks/pages
  - Poll status field for each page

# 6. Stream audio when ready
GET /user/books/{id}/pages/{page}/audio
```

### Example 2: Payment Flow

```bash
# 1. Check account type
GET /user/account-type → "free" or "paid"

# 2. Generate checkout URL
POST /user/stripe/create-checkout-session
  - Creates Stripe customer if needed
  - Returns checkout URL

# 3. User pays on Stripe-hosted page
# Stripe redirects to success/cancel page

# 4. Stripe notifies backend
POST /stripe/webhook
  - Updates user.account_type to "paid" or "free"
```

## External API Integrations

### OpenAI API
- **SSML generation** - GPT-4o converts text to SSML markup
- **Text-to-speech** - gpt-4o-mini-tts converts SSML to MP3
- **Music prompt generation** - GPT-4o analyzes book excerpt
- **Sound event extraction** - GPT-4o identifies Foley events
- **Mood segmentation** - GPT-4o creates time-based mood segments
- **Book cover search** - GPT-4o with web search finds official book covers (1000×1600px)

### ElevenLabs API
- **Sound effect generation** - 22-second clips based on prompts
- **Background music** - Instrumental music matching book mood
- **Foley effects** - sword_clash, door_creak, thunder, etc.

### Stripe API
- **Payment processing** - Checkout session creation
- **Subscription management** - Track active subscriptions
- **Webhook events** - Handle payment success/cancellation

### MQTT Broker (Mosquitto)
- **Real-time events** - Publish updates to connected clients
- **Topics**: user-specific and debug channels
- **TLS support** - Secure communication in production

## Security Considerations

**Implemented**:
- JWT tokens with HMAC-SHA256 signing (72 hour expiry)
- Bcrypt password hashing (DefaultCost = ~10 rounds)
- Database SSL/TLS in production (DB_SSLMODE=require)
- MQTT TLS support with optional cert verification
- Access control: Users can only stream/modify their own books
- File upload validation: File type and size restrictions
- Input validation: Required fields, email format checks

**Notes**:
- Stripe API keys stored in environment variables (standard practice)
- Rate limiting not implemented (could be added to gateway)

## Project Metrics

- **Total Codebase**: ~3,170 lines of Go code
- **Services**: 3 (gateway, auth, content)
- **Database Tables**: 5
- **API Endpoints**: 20+
- **External API Calls**: OpenAI (x3), ElevenLabs (x1), Stripe (x2)
- **FFmpeg Operations**: ~6 different filter chains
- **Concurrent Processing**: Goroutines for TTS, music, effects

## Development Notes

### Key Scripts
- [wait-for-postgres.sh](auth-service/wait-for-postgres.sh) - Database readiness check
- [setup-server.sh](setup-server.sh) - Initial server configuration
- [mosquitto.conf](mosquitto.conf) - MQTT broker configuration

### Performance Optimizations
- Go routines for async processing
- Content hash caching for duplicate content
- FFmpeg multi-format output (MP3, Opus)
- Compression: libmp3lame (quality 2), libopus (64kbps)

### Error Handling
- Fallback music segmentation (equal-length segments)
- Graceful degradation if GPT fails
- MQTT non-blocking (continues without broker)
- FFmpeg error logging with detailed output

## Complete User Journey

```
User → Gateway (8080) → Routes to Auth Service (8082)
  ↓
signup/login → JWT token with user_id claim
  ↓
User → Content Service (8083) with token
  ↓
Create book → Upload file (PDF/TXT/EPUB)
  ↓
Auto-chunk & store in PostgreSQL
  ↓
Request batch TTS → Async processing:
  • For each chunk:
    - Text → SSML (GPT-4)
    - SSML → MP3 (OpenAI TTS)
    - MP3 + Music → Mixed (ElevenLabs + FFmpeg)
    - Mixed + Effects → Final (ElevenLabs + FFmpeg)
  • Update status → Publish MQTT event
  ↓
Poll GET /chunks/pages until all completed
  ↓
Stream GET /pages/{page}/audio with token
  ↓
Share URL requires token (query param or header)
```

## Conclusion

This is a **production-ready, feature-rich audiobook streaming platform** with:
- Scalable microservices architecture
- AI-powered content generation (TTS, music, sound effects)
- Real-time event system (MQTT)
- Payment integration (Stripe)
- Multi-format document support (PDF, TXT, EPUB)
- Persistent storage with Docker volumes
- Comprehensive API for book management and streaming
- Professional deployment with health checks and logging

The project demonstrates advanced Go practices including error handling, concurrency patterns, API integration, database design, and containerization best practices.
