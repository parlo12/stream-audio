# Deployment Checklist - Book Search Feature

## 📋 Pre-Deployment Checklist

### Local Testing (Before Git Push)

- [ ] Code compiles without errors
- [ ] All new files are included in git
- [ ] Environment variables are documented
- [ ] Test script runs successfully

### Files to Commit

```bash
# New files
content-service/book_search.go
BOOK_SEARCH_API.md
test-book-search.sh
DEPLOYMENT_CHECKLIST.md

# Modified files
content-service/main.go
IOS-CONNECT.md
```

---

## 🚀 Deployment Steps

### Step 1: Verify Local Environment

```bash
# Navigate to project directory
cd /Users/rolflouisdor/Desktop/RMH-Real-Estate/stream-audio

# Check git status
git status

# You should see:
# - content-service/book_search.go (new file)
# - content-service/main.go (modified)
# - BOOK_SEARCH_API.md (new file)
# - IOS-CONNECT.md (modified)
# - test-book-search.sh (new file)
# - DEPLOYMENT_CHECKLIST.md (new file)
```

### Step 2: Stage and Commit Changes

```bash
# Add all new and modified files
git add content-service/book_search.go
git add content-service/main.go
git add BOOK_SEARCH_API.md
git add IOS-CONNECT.md
git add test-book-search.sh
git add DEPLOYMENT_CHECKLIST.md

# Commit with descriptive message
git commit -m "feat: Add AI-powered book search/discovery endpoint

- Add POST /user/search-books endpoint
- Uses OpenAI GPT-4o with web search for book discovery
- Returns up to 5 book suggestions with covers and summaries
- JWT authentication required
- Reuses existing OPENAI_API_KEY environment variable
- Add comprehensive API documentation and test script
- Update iOS integration guide with new endpoint

Files added:
- content-service/book_search.go
- BOOK_SEARCH_API.md
- test-book-search.sh
- DEPLOYMENT_CHECKLIST.md

Files modified:
- content-service/main.go (add route)
- IOS-CONNECT.md (update version and add endpoint docs)"
```

### Step 3: Push to Remote

```bash
# Check current branch
git branch

# Push to main branch (or your target branch)
git push origin main

# If you get an error about upstream, use:
# git push -u origin main
```

### Step 4: SSH into Production Server

```bash
# SSH into your production server
ssh user@68.183.22.205

# Or if you have a specific key:
# ssh -i ~/.ssh/your-key.pem user@68.183.22.205
```

### Step 5: Pull Changes on Server

```bash
# Navigate to project directory
cd /path/to/stream-audio

# Pull latest changes
git pull origin main

# Verify files are updated
ls -la content-service/book_search.go
cat content-service/main.go | grep "search-books"
```

### Step 6: Rebuild and Restart Services

```bash
# Stop current services
docker-compose -f docker-compose.prod.yml down content-service

# Rebuild content-service
docker-compose -f docker-compose.prod.yml build content-service

# Start services
docker-compose -f docker-compose.prod.yml up -d content-service

# Wait 10 seconds for service to start
sleep 10
```

### Step 7: Verify Deployment

```bash
# Check if container is running
docker ps | grep content-service

# Check logs for errors
docker logs content-service --tail 50

# Look for the route being registered
docker logs content-service | grep "search-books"

# Expected output:
# → POST /user/search-books
```

### Step 8: Test Endpoint

```bash
# First, login to get a token
curl -X POST http://68.183.22.205:8080/login \
  -H "Content-Type: application/json" \
  -d '{"username": "YOUR_USERNAME", "password": "YOUR_PASSWORD"}'

# Copy the token from response

# Test the search endpoint
curl -X POST http://68.183.22.205:8083/user/search-books \
  -H "Authorization: Bearer YOUR_TOKEN_HERE" \
  -H "Content-Type: application/json" \
  -d '{"query": "Harry Potter"}'

# Expected response:
# {"results": [{"title": "...", "author": "...", "cover_url": "...", "summary": "..."}]}
```

### Step 9: Run Test Script (Optional)

```bash
# On your local machine, run the test script
./test-book-search.sh

# Follow the prompts to test various scenarios
```

---

## ✅ Post-Deployment Verification

### Backend Checks

- [ ] Service is running: `docker ps | grep content-service`
- [ ] No errors in logs: `docker logs content-service --tail 100`
- [ ] Route is registered: `docker logs content-service | grep "search-books"`
- [ ] Endpoint responds: `curl` test returns valid JSON
- [ ] Authentication works: Unauthorized without token
- [ ] Search returns results: Valid query returns book suggestions

### Environment Checks

- [ ] `OPENAI_API_KEY` is set in production
- [ ] API key is valid and has credits
- [ ] Network connectivity to OpenAI API

```bash
# Check environment variable
docker exec content-service printenv OPENAI_API_KEY

# Should output: sk-...your-key...
```

### API Tests

- [ ] Test valid search: `{"query": "Harry Potter"}`
- [ ] Test missing query: `{}` (should return 400)
- [ ] Test empty query: `{"query": ""}` (should return 400)
- [ ] Test missing token: No Authorization header (should return 401)
- [ ] Test invalid token: Wrong token (should return 401)

---

## 📊 Monitoring

### Check Logs

```bash
# Real-time logs
docker logs -f content-service

# Last 100 lines
docker logs content-service --tail 100

# Search for errors
docker logs content-service | grep -i error

# Search for search endpoint activity
docker logs content-service | grep "search-books"
```

### Performance Metrics

Monitor response times in production:
- Normal: 5-15 seconds
- Slow: 15-30 seconds
- Timeout: 60 seconds

If consistently slow:
- Check OpenAI API status: https://status.openai.com/
- Check network latency to OpenAI servers
- Review server resources: `docker stats content-service`

---

## 🐛 Troubleshooting

### Issue: Endpoint not found (404)

**Cause:** Route not registered or service not restarted

**Solution:**
```bash
# Check if route is registered
docker logs content-service | grep "search-books"

# If not found, rebuild and restart
docker-compose -f docker-compose.prod.yml up -d --build content-service
```

### Issue: "OPENAI_API_KEY not set" error

**Cause:** Environment variable not configured

**Solution:**
```bash
# Check if key is set
docker exec content-service printenv OPENAI_API_KEY

# If empty, add to docker-compose.prod.yml or .env
# Then restart service
docker-compose -f docker-compose.prod.yml up -d content-service
```

### Issue: No results returned

**Cause:** OpenAI API error, rate limit, or parsing issue

**Solution:**
```bash
# Check logs for detailed error
docker logs content-service --tail 100 | grep -A 10 "search-books"

# Check OpenAI API status
curl https://status.openai.com/api/v2/status.json

# Verify API key works
curl https://api.openai.com/v1/models \
  -H "Authorization: Bearer $OPENAI_API_KEY"
```

### Issue: Slow response times

**Cause:** OpenAI API latency or network issues

**Solution:**
- Check network: `ping api.openai.com`
- Check OpenAI status: https://status.openai.com/
- Monitor server resources: `docker stats`
- Consider adding caching for common queries

---

## 📝 Rollback Plan

If something goes wrong:

### Option 1: Quick Rollback

```bash
# Revert to previous commit
git revert HEAD

# Push revert
git push origin main

# On server
cd /path/to/stream-audio
git pull origin main
docker-compose -f docker-compose.prod.yml up -d --build content-service
```

### Option 2: Full Rollback

```bash
# Find previous working commit
git log --oneline

# Reset to that commit
git reset --hard COMMIT_HASH

# Force push (use with caution)
git push -f origin main

# On server
cd /path/to/stream-audio
git fetch origin
git reset --hard origin/main
docker-compose -f docker-compose.prod.yml up -d --build content-service
```

---

## 📞 Support Contacts

If issues persist:

1. **Check Documentation:**
   - [BOOK_SEARCH_API.md](BOOK_SEARCH_API.md)
   - [IOS-CONNECT.md](IOS-CONNECT.md)

2. **Review Logs:**
   - Backend: `docker logs content-service`
   - Gateway: `docker logs gateway`

3. **OpenAI Support:**
   - Status: https://status.openai.com/
   - Help: https://help.openai.com/

---

## ✨ Success Criteria

Deployment is successful when:

- [x] Code pushed to remote repository
- [x] Production server updated with latest code
- [x] Docker container rebuilt and running
- [x] Endpoint accessible at `http://68.183.22.205:8083/user/search-books`
- [x] Authentication required and working
- [x] Valid searches return book results
- [x] Error cases handled correctly (400, 401)
- [x] Logs show no errors
- [x] iOS team notified and documentation shared

---

## 📅 Deployment Timeline

**Estimated Time:** 15-30 minutes

1. Local testing: 5 minutes
2. Git commit and push: 2 minutes
3. SSH and pull on server: 2 minutes
4. Docker rebuild: 3-5 minutes
5. Testing and verification: 5-10 minutes
6. Documentation update: 5 minutes

---

## 🎯 Next Steps After Deployment

1. **Notify iOS Team:**
   - Share [BOOK_SEARCH_API.md](BOOK_SEARCH_API.md)
   - Share updated [IOS-CONNECT.md](IOS-CONNECT.md)
   - Provide test credentials

2. **Monitor Usage:**
   - Track API calls in logs
   - Monitor OpenAI API usage/costs
   - Collect user feedback

3. **Future Enhancements:**
   - Add caching for popular queries
   - Implement rate limiting
   - Add pagination for more results
   - Add filters (genre, year, etc.)

---

**Deployment Date:** December 12, 2025
**Deployed By:** Backend Team
**Version:** 1.1
**Status:** Ready for Production ✅
