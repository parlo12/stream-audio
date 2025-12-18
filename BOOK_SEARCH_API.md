# Book Search API Documentation

## Overview

The Book Search API allows iOS users to search for books and get AI-generated suggestions with covers and summaries using OpenAI's web search capabilities.

**Added:** December 12, 2025
**Version:** 1.0
**Status:** Production Ready ✅

---

## Endpoint Details

### Search Books

**URL:** `POST /user/search-books`

**Service:** Content Service (Port 8083)

**Base URL:** `http://68.183.22.205:8083`

**Authentication:** Required (JWT Bearer token)

---

## Request

### Headers

```
Authorization: Bearer {jwt_token}
Content-Type: application/json
```

### Body

```json
{
  "query": "Harry Potter"
}
```

### Parameters

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `query` | string | ✅ Yes | The book title or search term |

**Example Queries:**
- `"Harry Potter"` - Searches for Harry Potter books
- `"Stephen King horror"` - Searches for Stephen King horror novels
- `"The Lord of the Rings"` - Searches for LOTR books
- `"science fiction classics"` - Broad genre search

---

## Response

### Success (200 OK)

```json
{
  "results": [
    {
      "title": "Harry Potter and the Philosopher's Stone",
      "author": "J.K. Rowling",
      "cover_url": "https://covers.openlibrary.org/b/id/8739161-L.jpg",
      "summary": "A young wizard discovers his magical heritage and attends Hogwarts School of Witchcraft and Wizardry, where he uncovers the truth about his parents' death and faces the dark wizard who killed them."
    },
    {
      "title": "Harry Potter and the Chamber of Secrets",
      "author": "J.K. Rowling",
      "cover_url": "https://covers.openlibrary.org/b/id/7984916-L.jpg",
      "summary": "In his second year at Hogwarts, Harry faces a mysterious monster that is petrifying students."
    }
  ]
}
```

### Result Object Schema

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Full book title |
| `author` | string | Author's full name |
| `cover_url` | string | Direct URL to book cover image |
| `summary` | string | 1-2 sentence book summary |

**Notes:**
- Returns up to 5 results per query
- Cover URLs are direct image links from reputable sources (Amazon, Goodreads, OpenLibrary)
- Results are AI-generated in real-time based on web search

---

## Error Responses

### 400 Bad Request - Missing Query

```json
{
  "error": "Query parameter is required"
}
```

### 400 Bad Request - Empty Query

```json
{
  "error": "Query cannot be empty"
}
```

### 401 Unauthorized - Missing/Invalid Token

```json
{
  "error": "Missing token"
}
```

**or**

```json
{
  "error": "Invalid token"
}
```

### 500 Internal Server Error

```json
{
  "error": "Failed to search books",
  "details": "Error message here"
}
```

**Common causes:**
- OpenAI API key not configured
- OpenAI API rate limit exceeded
- Network connectivity issues
- Invalid OpenAI API response

---

## Implementation Details

### Technology Stack

- **AI Model:** OpenAI GPT-4o with web search tool
- **API:** OpenAI Responses API (`https://api.openai.com/v1/responses`)
- **Image Sources:** Amazon, Goodreads, OpenLibrary, publisher websites
- **Authentication:** JWT tokens (same as other endpoints)

### Code Files

- **Handler:** [content-service/book_search.go](content-service/book_search.go)
- **Route Registration:** [content-service/main.go:176](content-service/main.go#L176)

### Environment Variables

```bash
OPENAI_API_KEY=<your-openai-api-key>  # Required
```

**Note:** Reuses the same `OPENAI_API_KEY` used for TTS and cover fetching.

### Performance

- **Response Time:** 5-15 seconds (depends on OpenAI API)
- **Timeout:** 60 seconds
- **Results:** Up to 5 books per search

---

## Testing

### cURL Examples

#### Basic Search

```bash
curl -X POST http://68.183.22.205:8083/user/search-books \
  -H "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..." \
  -H "Content-Type: application/json" \
  -d '{"query": "Harry Potter"}'
```

#### Search with Different Query

```bash
curl -X POST http://68.183.22.205:8083/user/search-books \
  -H "Authorization: Bearer {your_jwt_token}" \
  -H "Content-Type: application/json" \
  -d '{"query": "The Lord of the Rings"}'
```

#### Test Missing Query (should fail)

```bash
curl -X POST http://68.183.22.205:8083/user/search-books \
  -H "Authorization: Bearer {your_jwt_token}" \
  -H "Content-Type: application/json" \
  -d '{}'
```

Expected response:
```json
{
  "error": "Query parameter is required"
}
```

#### Test Missing Token (should fail)

```bash
curl -X POST http://68.183.22.205:8083/user/search-books \
  -H "Content-Type: application/json" \
  -d '{"query": "Harry Potter"}'
```

Expected response:
```json
{
  "error": "Missing token"
}
```

---

## iOS Integration

### Swift Model

```swift
struct BookSearchRequest: Codable {
    let query: String
}

struct BookSuggestion: Codable, Identifiable {
    let title: String
    let author: String
    let cover_url: String
    let summary: String

    var id: String { title + author }

    enum CodingKeys: String, CodingKey {
        case title, author, summary
        case cover_url = "cover_url"
    }
}

struct BookSearchResponse: Codable {
    let results: [BookSuggestion]
}
```

### API Client Function

```swift
class APIClient {
    func searchBooks(query: String, completion: @escaping (Result<[BookSuggestion], Error>) -> Void) {
        guard let token = KeychainSwift().get("auth_token") else {
            completion(.failure(NSError(domain: "AuthError", code: 401,
                userInfo: [NSLocalizedDescriptionKey: "No auth token"])))
            return
        }

        let url = URL(string: "http://68.183.22.205:8083/user/search-books")!
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        let body = BookSearchRequest(query: query)
        request.httpBody = try? JSONEncoder().encode(body)

        URLSession.shared.dataTask(with: request) { data, response, error in
            if let error = error {
                completion(.failure(error))
                return
            }

            guard let httpResponse = response as? HTTPURLResponse else {
                completion(.failure(NSError(domain: "NetworkError", code: 0)))
                return
            }

            guard 200...299 ~= httpResponse.statusCode else {
                let apiError = APIError(statusCode: httpResponse.statusCode, data: data)
                completion(.failure(apiError))
                return
            }

            guard let data = data else {
                completion(.failure(NSError(domain: "NoDataError", code: 0)))
                return
            }

            do {
                let response = try JSONDecoder().decode(BookSearchResponse.self, from: data)
                completion(.success(response.results))
            } catch {
                completion(.failure(error))
            }
        }.resume()
    }
}
```

### SwiftUI View Example

```swift
struct BookSearchView: View {
    @State private var searchQuery = ""
    @State private var searchResults: [BookSuggestion] = []
    @State private var isSearching = false
    @State private var errorMessage: String?

    var body: some View {
        NavigationView {
            VStack {
                // Search Bar
                HStack {
                    TextField("Search for books...", text: $searchQuery)
                        .textFieldStyle(RoundedBorderTextFieldStyle())
                        .onSubmit {
                            searchBooks()
                        }

                    Button("Search") {
                        searchBooks()
                    }
                    .disabled(searchQuery.isEmpty || isSearching)
                }
                .padding()

                // Loading Indicator
                if isSearching {
                    ProgressView("Searching...")
                        .padding()
                }

                // Error Message
                if let error = errorMessage {
                    Text(error)
                        .foregroundColor(.red)
                        .padding()
                }

                // Results List
                List(searchResults) { book in
                    BookSearchRow(book: book)
                        .onTapGesture {
                            // Handle book selection
                            selectBook(book)
                        }
                }
            }
            .navigationTitle("Discover Books")
        }
    }

    func searchBooks() {
        guard !searchQuery.isEmpty else { return }

        isSearching = true
        errorMessage = nil

        APIClient.shared.searchBooks(query: searchQuery) { result in
            DispatchQueue.main.async {
                isSearching = false

                switch result {
                case .success(let books):
                    searchResults = books
                    if books.isEmpty {
                        errorMessage = "No books found for '\(searchQuery)'"
                    }

                case .failure(let error):
                    errorMessage = "Search failed: \(error.localizedDescription)"
                    searchResults = []
                }
            }
        }
    }

    func selectBook(_ book: BookSuggestion) {
        // Create a book in the user's library
        APIClient.shared.createBook(
            title: book.title,
            author: book.author,
            category: "Fiction", // Default or let user choose
            genre: nil
        ) { result in
            // Handle book creation
        }
    }
}

struct BookSearchRow: View {
    let book: BookSuggestion

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            // Cover Image
            AsyncImage(url: URL(string: book.cover_url)) { phase in
                switch phase {
                case .empty:
                    ProgressView()
                        .frame(width: 60, height: 96)
                case .success(let image):
                    image
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 60, height: 96)
                        .cornerRadius(4)
                case .failure:
                    Rectangle()
                        .fill(Color.gray.opacity(0.3))
                        .frame(width: 60, height: 96)
                        .cornerRadius(4)
                        .overlay(
                            Image(systemName: "book.closed")
                                .foregroundColor(.gray)
                        )
                @unknown default:
                    EmptyView()
                }
            }

            // Book Info
            VStack(alignment: .leading, spacing: 4) {
                Text(book.title)
                    .font(.headline)
                    .lineLimit(2)

                Text(book.author)
                    .font(.subheadline)
                    .foregroundColor(.secondary)

                Text(book.summary)
                    .font(.caption)
                    .foregroundColor(.secondary)
                    .lineLimit(3)
                    .padding(.top, 4)
            }
        }
        .padding(.vertical, 8)
    }
}
```

---

## Use Cases

### 1. Book Discovery
Users can search for books they want to convert to audiobooks:
- Search by title: "Harry Potter"
- Search by author: "Stephen King"
- Search by genre: "science fiction classics"

### 2. Autocomplete/Suggestions
As users type a book title during creation, show suggestions:
```swift
// Debounce search as user types
.onChange(of: bookTitle) { newValue in
    searchDebouncer.debounce {
        searchBooks(query: newValue)
    }
}
```

### 3. Quick Add from Search
Users can tap a search result to immediately create a book in their library with pre-filled title and author.

### 4. Browse Popular Books
Show curated searches on the home screen:
- "Best sellers 2025"
- "Classic literature"
- "New releases"

---

## Testing Checklist

### Backend Tests

- [x] Endpoint registered at `/user/search-books`
- [x] JWT authentication required
- [x] Valid search query returns results
- [x] Missing query returns 400 error
- [x] Empty query returns 400 error
- [x] Missing token returns 401 error
- [x] Invalid token returns 401 error
- [x] OpenAI API integration working
- [x] Cover URLs are valid and accessible
- [x] Results parsed correctly from OpenAI response

### iOS Tests

- [ ] Search bar accepts input
- [ ] Search button triggers API call
- [ ] Loading indicator shows during search
- [ ] Results display in list
- [ ] Cover images load correctly
- [ ] Tap on result navigates to detail/creation
- [ ] Error messages display correctly
- [ ] Empty results show appropriate message
- [ ] Network errors handled gracefully

### Manual Test Scenarios

1. **Basic Search**
   - Query: "Harry Potter"
   - Expected: 3-5 Harry Potter books with covers and summaries

2. **Author Search**
   - Query: "Stephen King"
   - Expected: Popular Stephen King books

3. **Genre Search**
   - Query: "science fiction classics"
   - Expected: Classic sci-fi books (Asimov, Clarke, etc.)

4. **Obscure Book**
   - Query: "The Name of the Wind Patrick Rothfuss"
   - Expected: At least 1 result with accurate cover

5. **Invalid Query**
   - Query: "" (empty)
   - Expected: 400 Bad Request error

6. **No Token**
   - Request without Authorization header
   - Expected: 401 Unauthorized error

---

## Troubleshooting

### Common Issues

#### 1. No Results Returned

**Possible Causes:**
- OpenAI API key not set
- OpenAI API rate limit exceeded
- Network connectivity issues

**Solution:**
```bash
# Check if OPENAI_API_KEY is set
docker exec content-service printenv OPENAI_API_KEY

# Check logs
docker logs content-service --tail 100 | grep "search-books"
```

#### 2. Invalid Cover URLs

**Possible Causes:**
- OpenAI returned non-image URLs
- URLs are expired/broken

**Solution:**
- The backend validates URLs but cannot guarantee they're always valid
- iOS should handle image loading failures gracefully with fallback placeholders

#### 3. Slow Response Time

**Possible Causes:**
- OpenAI API latency
- Web search taking longer than usual

**Solution:**
- Show loading indicator in iOS app
- Consider 60-second timeout
- Implement retry logic for failures

---

## Security

### API Key Protection

✅ **SECURE:** `OPENAI_API_KEY` is stored on the backend only (never exposed to iOS app)

✅ **SECURE:** JWT authentication required for all requests

✅ **SECURE:** User can only search books (no write operations)

### Rate Limiting

**Current:** No rate limiting implemented

**Recommended:** Add rate limiting to prevent abuse:
```go
// Example: 10 requests per minute per user
import "github.com/gin-contrib/rate"

router.Use(rate.RateLimiter(rate.Options{
    Limit: 10,
    Window: time.Minute,
}))
```

---

## Future Enhancements

### Potential Improvements

1. **Caching**
   - Cache popular search queries (e.g., "Harry Potter") for 24 hours
   - Reduces OpenAI API calls and improves response time

2. **Pagination**
   - Currently returns max 5 results
   - Could add pagination for more results

3. **Filters**
   - Add genre filter: `{"query": "fantasy", "genre": "fantasy"}`
   - Add year filter: `{"query": "2024 releases"}`
   - Add author filter: `{"author": "Stephen King"}`

4. **Search History**
   - Store user's search history
   - Show recent searches

5. **Trending Books**
   - Endpoint for trending/popular books
   - No query parameter needed

6. **Book Details**
   - Add ISBN, publication date, page count
   - Add ratings/reviews

---

## Deployment Notes

### Pushing to Production

```bash
# 1. Commit changes
git add content-service/book_search.go
git add content-service/main.go
git add BOOK_SEARCH_API.md
git commit -m "feat: Add book search/discovery endpoint with AI-powered suggestions"

# 2. Push to remote
git push origin main

# 3. On production server, pull and rebuild
ssh user@68.183.22.205
cd /path/to/stream-audio
git pull origin main
docker-compose -f docker-compose.prod.yml up -d --build content-service

# 4. Verify deployment
docker logs content-service --tail 50 | grep "search-books"

# 5. Test endpoint
curl -X POST http://68.183.22.205:8083/user/search-books \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{"query": "Harry Potter"}'
```

### Environment Variables

Ensure `OPENAI_API_KEY` is set in production:

```bash
# In .env or docker-compose.prod.yml
OPENAI_API_KEY=sk-...your-key-here...
```

---

## API Changelog

### Version 1.0 (December 12, 2025)

**Added:**
- New endpoint `POST /user/search-books`
- AI-powered book search using OpenAI GPT-4o with web search
- Returns up to 5 book suggestions with covers and summaries
- JWT authentication required
- Reuses existing `OPENAI_API_KEY` environment variable

**Files Changed:**
- Created: `content-service/book_search.go`
- Modified: `content-service/main.go` (added route)
- Created: `BOOK_SEARCH_API.md` (documentation)

---

## Support

For issues or questions:

1. **Check logs:**
   ```bash
   docker logs content-service --tail 100 | grep "search-books"
   ```

2. **Verify OpenAI API key:**
   ```bash
   docker exec content-service printenv OPENAI_API_KEY
   ```

3. **Test endpoint directly:**
   ```bash
   curl -X POST http://68.183.22.205:8083/user/search-books \
     -H "Authorization: Bearer {token}" \
     -H "Content-Type: application/json" \
     -d '{"query": "test"}'
   ```

4. **Check OpenAI API status:** https://status.openai.com/

---

**Last Updated:** December 12, 2025
**Maintained By:** Backend Team
**Production URL:** `http://68.183.22.205:8083/user/search-books`
