# content-service

Book processing and audio generation for Narrafied (port 8083).

Handles document upload and chunking (PDF/TXT/EPUB/MOBI/AZW/AZW3), text-to-speech
via OpenAI, dynamic background music + ambient + Foley via ElevenLabs/FFmpeg,
authenticated audio streaming, book-cover fetching, and MQTT event publishing.
All book/chunk endpoints are owner-scoped (see `ownership.go`).

See the repository root `CLAUDE.md` and `appFixPlan.md` for architecture and this
service's place in the system.
