# Automatic Book Cover Fetching - Deployment Guide

## Overview

This guide provides step-by-step instructions for deploying the new automatic book cover fetching feature to your production server.

## What's New

The system now automatically fetches book covers from the web when users create new books, eliminating the need for manual cover uploads. This uses OpenAI's Responses API with web search capability.

## Files Modified/Created

### New Files:
- `content-service/bookCoverWebSearch.go` - Web search and cover download logic

### Modified Files:
- `content-service/main.go` - Integrated automatic cover fetching into book creation
- `claude.md` - Updated project documentation
- `API_CHANGELOG.md` - Added iOS integration guide for new feature

## Prerequisites

Before deploying, ensure:
1. ✅ `OPENAI_API_KEY` is set in your production `.env` file
2. ✅ OpenAI API account has access to the Responses API
3. ✅ `/opt/stream-audio-data/covers` directory exists with proper permissions
4. ✅ Production server has internet access for web searches

## Deployment Steps

### Step 1: Commit Changes to Git

```bash
# Navigate to project directory
cd /Users/rolflouisdor/Desktop/RMH-Real-Estate/stream-audio

# Check git status
git status

# Add all changes
git add content-service/bookCoverWebSearch.go
git add content-service/main.go
git add claude.md
git add API_CHANGELOG.md
git add AUTOMATIC_COVER_DEPLOYMENT.md

# Commit with descriptive message
git commit -m "feat: Add automatic book cover fetching via OpenAI web search

- Created bookCoverWebSearch.go for web search functionality
- Integrated automatic cover fetching into book creation flow
- Updated documentation (claude.md, API_CHANGELOG.md)
- Covers are fetched asynchronously in background
- Uses OpenAI Responses API with web search tool
- Target dimensions: 1000×1600px (aspect ratio 0.625)
- MQTT event published when cover is ready
- Manual upload endpoint still available as fallback"

# Push to main branch
git push origin main
```

### Step 2: SSH into Production Server

```bash
# Replace with your actual server IP/hostname
ssh user@your-production-server.com
```

### Step 3: Pull Latest Changes

```bash
# Navigate to project directory on server
cd /path/to/stream-audio

# Pull latest changes from main branch
git pull origin main

# Verify new files are present
ls -la content-service/bookCoverWebSearch.go
```

### Step 4: Verify Environment Variables

```bash
# Check that OPENAI_API_KEY is set
cat .env | grep OPENAI_API_KEY

# If missing, add it:
echo 'OPENAI_API_KEY=sk-your-key-here' >> .env
```

### Step 5: Rebuild and Restart Services

```bash
# Stop current services
docker compose -f docker-compose.prod.yml down

# Rebuild services (this will compile the new Go code)
docker compose -f docker-compose.prod.yml build --no-cache content-service

# Start services
docker compose -f docker-compose.prod.yml up -d

# Verify services are running
docker compose -f docker-compose.prod.yml ps
```

### Step 6: Monitor Logs

```bash
# Watch content-service logs for any errors
docker compose -f docker-compose.prod.yml logs -f content-service

# You should see:
# - "Content service listening on port 8083"
# - No compilation errors
# - Database connected successfully
```

## Testing the Feature

### Test 1: Create a Book via API

```bash
# First, get a JWT token (replace with actual credentials)
TOKEN=$(curl -s -X POST http://your-server.com:8082/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"yourpassword"}' \
  | jq -r '.token')

echo "Token: $TOKEN"

# Create a new book
curl -X POST http://your-server.com:8083/user/books \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "title": "The Great Gatsby",
    "author": "F. Scott Fitzgerald",
    "category": "Fiction",
    "genre": "Classic"
  }'

# Expected response:
# {
#   "message": "Book saved, cover fetching in progress",
#   "book": {
#     "id": 123,
#     "title": "The Great Gatsby",
#     ...
#   }
# }
```

### Test 2: Monitor Logs for Cover Fetching

```bash
# Watch logs for cover fetching activity
docker compose -f docker-compose.prod.yml logs -f content-service | grep -i cover

# You should see:
# 🔍 Fetching book cover for 'The Great Gatsby' by F. Scott Fitzgerald...
# ✅ Found book cover URL: https://...
# ✅ Book cover downloaded and saved: ./uploads/covers/123_1733097600.jpg
# ✅ Book cover automatically fetched and saved for book ID 123
```

### Test 3: Verify Cover Was Saved

```bash
# List covers directory
docker compose -f docker-compose.prod.yml exec content-service ls -la /app/uploads/covers/

# You should see a file like: 123_1733097600.jpg
```

### Test 4: Fetch Book to Verify Cover URL

```bash
# Get book details
curl -X GET http://your-server.com:8083/user/books/123 \
  -H "Authorization: Bearer $TOKEN" \
  | jq '.book.cover_url'

# Expected output:
# "https://your-server.com:8083/covers/123_1733097600.jpg"
```

### Test 5: Access Cover Image

```bash
# Download and verify the cover image
curl -o /tmp/cover.jpg "https://your-server.com:8083/covers/123_1733097600.jpg"

# Check file size (should be at least 5KB)
ls -lh /tmp/cover.jpg

# Optionally, open it to verify it's a valid image
```

### Test 6: MQTT Event Verification (Optional)

If you have MQTT client installed:

```bash
# Subscribe to MQTT topic (replace with your broker details)
mosquitto_sub -h your-mqtt-broker.com -t "users/+/cover_uploaded" -v

# You should see events like:
# users/456/cover_uploaded {
#   "book_id": 123,
#   "cover_url": "https://...",
#   "timestamp": "2025-12-01T12:00:00Z",
#   "source": "web_search"
# }
```

## Troubleshooting

### Issue: "OPENAI_API_KEY environment variable not set"

**Solution:**
```bash
# Add key to .env file
echo 'OPENAI_API_KEY=sk-your-actual-key' >> .env

# Restart services
docker compose -f docker-compose.prod.yml restart content-service
```

### Issue: "Failed to find book cover: no valid image URL found"

**Possible causes:**
1. OpenAI API returned unexpected response format
2. Web search didn't find a suitable cover
3. Network connectivity issues

**Solution:**
```bash
# Check logs for detailed error
docker compose -f docker-compose.prod.yml logs content-service | grep -A 5 "Failed to find book cover"

# Verify OpenAI API is accessible
docker compose -f docker-compose.prod.yml exec content-service curl -I https://api.openai.com

# Try manual upload as fallback
curl -X POST http://your-server.com:8083/user/books/123/cover \
  -H "Authorization: Bearer $TOKEN" \
  -F "cover=@/path/to/local/cover.jpg"
```

### Issue: "Failed to download image: HTTP status 403/404"

**Possible causes:**
1. Image URL is behind authentication
2. Image URL expired
3. Direct linking not allowed

**Solution:**
- The system will log the error but won't fail book creation
- Users can manually upload a cover using the legacy endpoint

### Issue: Compilation errors after deployment

**Solution:**
```bash
# Check Go build errors
docker compose -f docker-compose.prod.yml logs content-service | grep -i error

# Rebuild with verbose output
docker compose -f docker-compose.prod.yml build --no-cache --progress=plain content-service

# If specific errors appear, check:
# - Go version compatibility (should be 1.22+)
# - Missing imports
# - Syntax errors
```

### Issue: Covers directory permission denied

**Solution:**
```bash
# Fix permissions on host
sudo chown -R $USER:$USER /opt/stream-audio-data/covers
sudo chmod -R 755 /opt/stream-audio-data/covers

# Or inside container
docker compose -f docker-compose.prod.yml exec content-service chmod -R 755 /app/uploads/covers
```

## Rollback Plan

If you need to rollback the changes:

```bash
# On production server
cd /path/to/stream-audio

# Revert to previous commit
git log --oneline -5  # Find commit hash before the feature
git reset --hard <previous-commit-hash>

# Rebuild and restart
docker compose -f docker-compose.prod.yml up -d --build --force-recreate
```

## Performance Monitoring

### Expected Metrics:
- **Cover fetch time**: 5-15 seconds (average)
- **Success rate**: 85-95% (depending on book obscurity)
- **Image size**: 50KB - 500KB (typical)
- **API cost**: ~$0.01 per cover search (OpenAI Responses API pricing)

### Monitor API Usage:
```bash
# Check OpenAI API usage in logs
docker compose -f docker-compose.prod.yml logs content-service | grep "OpenAI API error"

# Count successful vs failed cover fetches
docker compose -f docker-compose.prod.yml logs content-service | grep -c "Book cover automatically fetched"
docker compose -f docker-compose.prod.yml logs content-service | grep -c "Failed to fetch book cover"
```

## Post-Deployment Checklist

- [ ] Services started successfully without errors
- [ ] Test book creation returns "cover fetching in progress" message
- [ ] Logs show cover search activity
- [ ] Cover images are saved to `/app/uploads/covers/`
- [ ] Cover URLs are accessible via HTTP
- [ ] MQTT events are published (if MQTT is configured)
- [ ] Book records are updated with cover_url and cover_path
- [ ] Manual upload endpoint still works as fallback
- [ ] No performance degradation in book creation API
- [ ] OpenAI API key is working and has sufficient credits

## Next Steps for iOS Team

Once deployed and verified, notify the iOS team to:
1. Review the [API_CHANGELOG.md](API_CHANGELOG.md) for implementation details
2. Implement MQTT listeners for `users/{user_id}/cover_uploaded` events
3. Update book creation UI to show cover loading state
4. Add polling fallback for cover updates
5. Implement manual upload option if automatic fetch fails

## Support

If you encounter issues during deployment:
1. Check logs: `docker compose -f docker-compose.prod.yml logs -f content-service`
2. Verify environment variables: `docker compose -f docker-compose.prod.yml config`
3. Check disk space: `df -h /opt/stream-audio-data`
4. Verify network connectivity: `docker compose -f docker-compose.prod.yml exec content-service ping -c 3 api.openai.com`

## Summary

This feature enhances user experience by automatically finding and downloading book covers from the web using OpenAI's advanced search capabilities. It's fully backward compatible, non-blocking, and includes fallback options for edge cases.

**Key Benefits:**
- ✅ Eliminates manual cover upload step
- ✅ Improves user experience
- ✅ Leverages OpenAI's web search for accuracy
- ✅ Asynchronous processing (non-blocking)
- ✅ MQTT events for real-time updates
- ✅ Fallback to manual upload if needed
- ✅ Production-ready with error handling
