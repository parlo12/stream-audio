# Quick Reference - Book Search Feature

## 🚀 One-Line Deploy

```bash
git add content-service/book_search.go content-service/main.go BOOK_SEARCH_API.md IOS-CONNECT.md test-book-search.sh DEPLOYMENT_CHECKLIST.md FEATURE_SUMMARY.md QUICK_REFERENCE.md && git commit -m "feat: Add AI-powered book search endpoint" && git push origin main
```

---

## 📍 New Endpoint

**URL:** `POST http://68.183.22.205:8083/user/search-books`

**Request:**
```json
{"query": "Harry Potter"}
```

**Response:**
```json
{
  "results": [
    {
      "title": "Book Title",
      "author": "Author Name",
      "cover_url": "https://...",
      "summary": "Summary text"
    }
  ]
}
```

---

## 🧪 Quick Test

```bash
# 1. Get token
TOKEN=$(curl -s -X POST http://68.183.22.205:8080/login \
  -H "Content-Type: application/json" \
  -d '{"username":"YOUR_USER","password":"YOUR_PASS"}' \
  | grep -o '"token":"[^"]*' | cut -d'"' -f4)

# 2. Search
curl -X POST http://68.183.22.205:8083/user/search-books \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": "Harry Potter"}'
```

---

## 📁 Files to Commit

```bash
git add content-service/book_search.go      # Main implementation
git add content-service/main.go             # Route registration
git add BOOK_SEARCH_API.md                  # Full API docs
git add IOS-CONNECT.md                      # iOS guide (updated)
git add test-book-search.sh                 # Test script
git add DEPLOYMENT_CHECKLIST.md             # Deployment guide
git add FEATURE_SUMMARY.md                  # Summary
git add QUICK_REFERENCE.md                  # This file
```

---

## 🔧 Deploy Commands

```bash
# On local machine
git push origin main

# On production server
ssh user@68.183.22.205
cd /path/to/stream-audio
git pull origin main
docker-compose -f docker-compose.prod.yml up -d --build content-service
docker logs content-service | grep "search-books"
```

---

## 📱 iOS Swift Code

```swift
func searchBooks(query: String, completion: @escaping (Result<[BookSuggestion], Error>) -> Void) {
    let url = URL(string: "http://68.183.22.205:8083/user/search-books")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")

    let body = ["query": query]
    request.httpBody = try? JSONSerialization.data(withJSONObject: body)

    URLSession.shared.dataTask(with: request) { data, response, error in
        // Handle response
    }.resume()
}
```

---

## 🐛 Troubleshooting

| Issue | Solution |
|-------|----------|
| 404 Not Found | Route not registered - rebuild container |
| 401 Unauthorized | Missing/invalid JWT token |
| 400 Bad Request | Empty query parameter |
| 500 Internal Error | Check `OPENAI_API_KEY` is set |
| Slow response | Normal 5-15s, check OpenAI status if >30s |

---

## 📚 Documentation

- **API Docs:** [BOOK_SEARCH_API.md](BOOK_SEARCH_API.md)
- **iOS Guide:** [IOS-CONNECT.md](IOS-CONNECT.md)
- **Deploy Guide:** [DEPLOYMENT_CHECKLIST.md](DEPLOYMENT_CHECKLIST.md)
- **Summary:** [FEATURE_SUMMARY.md](FEATURE_SUMMARY.md)

---

## ✅ Checklist

**Before Push:**
- [x] Code implemented
- [x] Files created
- [x] Documentation written

**After Deploy:**
- [ ] `git push` successful
- [ ] Server pulled latest code
- [ ] Container rebuilt
- [ ] Endpoint responds
- [ ] iOS team notified

---

**Ready to deploy? Just run the one-liner at the top!** 🚀
