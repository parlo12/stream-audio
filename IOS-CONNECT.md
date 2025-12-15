# iOS Integration Guide - Stream Audio Microservice API

**Version:** 1.1
**Last Updated:** December 12, 2025
**Backend API Version:** Production v1.1
**Gateway Base URL:** `http://68.183.22.205:8080` (Production)

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Authentication](#authentication)
4. [API Endpoints](#api-endpoints)
5. [Core Workflows](#core-workflows)
6. [Audio Streaming](#audio-streaming)
7. [MQTT Real-Time Events](#mqtt-real-time-events)
8. [Data Models](#data-models)
9. [Error Handling](#error-handling)
10. [Code Examples](#code-examples)
11. [Best Practices](#best-practices)
12. [Testing Guide](#testing-guide)

---

## Overview

Stream Audio is a sophisticated audiobook platform that converts documents (PDF, TXT, EPUB, MOBI, AZW, AZW3) into high-quality audio with:
- **AI-powered Text-to-Speech** (OpenAI gpt-4o-mini-tts)
- **Dynamic background music generation** (ElevenLabs)
- **Foley sound effects** (sword clashes, door creaks, etc.)
- **Automatic book cover fetching** (OpenAI web search)
- **Real-time progress updates** (MQTT)
- **Subscription management** (Stripe)

### Key Features for iOS App

✅ User authentication with JWT (72-hour tokens)
✅ Book creation and file upload (multi-format support)
✅ Automatic cover image fetching
✅ Page-by-page or batch audio generation
✅ Audio streaming with AVPlayer support (token in URL query)
✅ Real-time MQTT notifications
✅ Subscription tier management (free/paid)

---

## Architecture

### Microservices Structure

```
┌─────────────────────────────────────────────────────────────┐
│                    Gateway Service (8080)                    │
│                  Entry Point for All Requests                │
└──────────────────────┬──────────────────────────────────────┘
                       │
        ┌──────────────┴──────────────┐
        │                             │
┌───────▼────────┐          ┌─────────▼──────────┐
│  Auth Service  │          │  Content Service   │
│    (Port 8082) │          │    (Port 8083)     │
│                │          │                    │
│ • Signup       │          │ • Book management  │
│ • Login        │          │ • File upload      │
│ • JWT tokens   │          │ • TTS processing   │
│ • Stripe       │          │ • Audio streaming  │
│ • User profile │          │ • MQTT events      │
└────────────────┘          └────────────────────┘
```

### Gateway Routing

All requests go through the **Gateway** at `http://68.183.22.205:8080`:

- `/signup` → Auth Service
- `/login` → Auth Service
- `/auth/*` → Auth Service
- `/content/*` → Content Service *(currently not in use)*
- `/user/*` → Content Service (direct access)

**Important:** For iOS, you'll primarily use:
- `http://68.183.22.205:8080/signup`
- `http://68.183.22.205:8080/login`
- `http://68.183.22.205:8083/user/*` (direct to content service)

---

## Authentication

### JWT Token System

The backend uses **JWT tokens** with HMAC-SHA256 signing.

#### Token Lifetime
- **Expiry:** 72 hours (3 days)
- **Issued At (iat):** Timestamp when token was created
- **Claims:** `username`, `user_id`, `exp`, `iat`

#### Token Storage (iOS)
```swift
// Store securely in Keychain
let keychain = KeychainSwift()
keychain.set(token, forKey: "auth_token")

// Retrieve
if let token = keychain.get("auth_token") {
    // Use token
}
```

#### Token Format
```
Header: Bearer {token}
```

### 1. User Signup

**Endpoint:** `POST /signup`

**Request:**
```json
{
  "username": "johndoe",
  "email": "john@example.com",
  "password": "securePassword123",
  "state": "California"
}
```

**Response (Success - 200 OK):**
```json
{
  "message": "User registered",
  "user_id": 42
}
```

**Response (Error - 400 Bad Request):**
```json
{
  "error": "User with this username or email already exists"
}
```

**Validation Rules:**
- `username`: Required, unique
- `email`: Required, unique, valid email format
- `password`: Required, minimum 6 characters
- `state`: Required

**Swift Example:**
```swift
struct SignupRequest: Codable {
    let username: String
    let email: String
    let password: String
    let state: String
}

func signup(username: String, email: String, password: String, state: String,
            completion: @escaping (Result<Int, Error>) -> Void) {
    let url = URL(string: "http://68.183.22.205:8080/signup")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")

    let body = SignupRequest(username: username, email: email,
                            password: password, state: state)
    request.httpBody = try? JSONEncoder().encode(body)

    URLSession.shared.dataTask(with: request) { data, response, error in
        if let error = error {
            completion(.failure(error))
            return
        }

        guard let data = data,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let userId = json["user_id"] as? Int else {
            completion(.failure(NSError(domain: "SignupError", code: 0)))
            return
        }

        completion(.success(userId))
    }.resume()
}
```

### 2. User Login

**Endpoint:** `POST /login`

**Request:**
```json
{
  "username": "johndoe",
  "password": "securePassword123"
}
```

**Response (Success - 200 OK):**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VybmFtZSI6ImpvaG5kb2UiLCJ1c2VyX2lkIjo0MiwiZXhwIjoxNzM1ODQzMjAwLCJpYXQiOjE3MzU1ODQwMDB9.signature"
}
```

**Response (Error - 401 Unauthorized):**
```json
{
  "error": "Invalid username or password"
}
```

**Swift Example:**
```swift
struct LoginRequest: Codable {
    let username: String
    let password: String
}

struct LoginResponse: Codable {
    let token: String
}

func login(username: String, password: String,
           completion: @escaping (Result<String, Error>) -> Void) {
    let url = URL(string: "http://68.183.22.205:8080/login")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")

    let body = LoginRequest(username: username, password: password)
    request.httpBody = try? JSONEncoder().encode(body)

    URLSession.shared.dataTask(with: request) { data, response, error in
        if let error = error {
            completion(.failure(error))
            return
        }

        guard let data = data else {
            completion(.failure(NSError(domain: "LoginError", code: 0)))
            return
        }

        do {
            let response = try JSONDecoder().decode(LoginResponse.self, from: data)

            // Store token securely
            let keychain = KeychainSwift()
            keychain.set(response.token, forKey: "auth_token")

            completion(.success(response.token))
        } catch {
            completion(.failure(error))
        }
    }.resume()
}
```

### 3. Get User Profile

**Endpoint:** `GET /user/profile`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 200 OK):**
```json
{
  "username": "johndoe",
  "email": "john@example.com",
  "account_type": "paid",
  "is_public": true,
  "state": "California",
  "books_read": 5,
  "created_at": "2025-01-15T10:30:00Z"
}
```

**Swift Example:**
```swift
struct UserProfile: Codable {
    let username: String
    let email: String
    let account_type: String  // "free" or "paid"
    let is_public: Bool
    let state: String
    let books_read: Int
    let created_at: String
}

func fetchUserProfile(completion: @escaping (Result<UserProfile, Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401)))
        return
    }

    let url = URL(string: "http://68.183.22.205:8080/user/profile")!
    var request = URLRequest(url: url)
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    URLSession.shared.dataTask(with: request) { data, response, error in
        if let error = error {
            completion(.failure(error))
            return
        }

        guard let data = data else {
            completion(.failure(NSError(domain: "ProfileError", code: 0)))
            return
        }

        do {
            let profile = try JSONDecoder().decode(UserProfile.self, from: data)
            completion(.success(profile))
        } catch {
            completion(.failure(error))
        }
    }.resume()
}
```

### 4. Check Account Type

**Endpoint:** `GET /user/account-type`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 200 OK):**
```json
{
  "account_type": "free"
}
```

**Usage:** Check subscription tier before allowing operations.

---

## API Endpoints

### Book Management

#### 1. Create Book

**Endpoint:** `POST /user/books`
**Service:** Content Service (Port 8083)

**Headers:**
```
Authorization: Bearer {token}
Content-Type: application/json
```

**Request:**
```json
{
  "title": "The Lord of the Rings",
  "author": "J.R.R. Tolkien",
  "category": "Fiction",
  "genre": "Fantasy"
}
```

**Response (Success - 200 OK):**
```json
{
  "message": "Book saved, cover fetching in progress",
  "book": {
    "ID": 123,
    "Title": "The Lord of the Rings",
    "Author": "J.R.R. Tolkien",
    "Category": "Fiction",
    "Genre": "Fantasy",
    "Content": "",
    "ContentHash": "",
    "FilePath": "",
    "AudioPath": "",
    "Status": "pending",
    "UserID": 42,
    "CoverPath": "",
    "CoverURL": "",
    "Index": 0,
    "CreatedAt": "2025-12-12T10:00:00Z",
    "UpdatedAt": "2025-12-12T10:00:00Z"
  }
}
```

**Important Notes:**
- Book cover is **automatically fetched** from the web in the background
- Cover fetching uses OpenAI web search (takes 5-15 seconds)
- MQTT event `users/{user_id}/cover_uploaded` is published when cover is ready
- `category` must be "Fiction" or "Non-Fiction"
- `genre` is optional

**Swift Example:**
```swift
struct BookCreateRequest: Codable {
    let title: String
    let author: String
    let category: String  // "Fiction" or "Non-Fiction"
    let genre: String?
}

struct Book: Codable {
    let ID: Int
    let Title: String
    let Author: String
    let Category: String
    let Genre: String?
    let Status: String
    let CoverURL: String?
    let CoverPath: String?
    let FilePath: String?
    let AudioPath: String?
}

func createBook(title: String, author: String, category: String, genre: String?,
                completion: @escaping (Result<Book, Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401)))
        return
    }

    let url = URL(string: "http://68.183.22.205:8083/user/books")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")

    let body = BookCreateRequest(title: title, author: author,
                                category: category, genre: genre)
    request.httpBody = try? JSONEncoder().encode(body)

    URLSession.shared.dataTask(with: request) { data, response, error in
        if let error = error {
            completion(.failure(error))
            return
        }

        guard let data = data,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let bookData = json["book"] as? [String: Any],
              let bookJSON = try? JSONSerialization.data(withJSONObject: bookData) else {
            completion(.failure(NSError(domain: "BookCreationError", code: 0)))
            return
        }

        do {
            let book = try JSONDecoder().decode(Book.self, from: bookJSON)
            completion(.success(book))
        } catch {
            completion(.failure(error))
        }
    }.resume()
}
```

#### 2. List Books

**Endpoint:** `GET /user/books`

**Headers:**
```
Authorization: Bearer {token}
```

**Query Parameters (Optional):**
- `category` - Filter by category (e.g., "Fiction")
- `genre` - Filter by genre (e.g., "Fantasy")

**Examples:**
- `GET /user/books` - Get all books
- `GET /user/books?category=Fiction` - Get Fiction books only
- `GET /user/books?category=Fiction&genre=Fantasy` - Get Fiction Fantasy books

**Response (Success - 200 OK):**
```json
{
  "books": [
    {
      "id": 123,
      "title": "The Lord of the Rings",
      "author": "J.R.R. Tolkien",
      "category": "Fiction",
      "genre": "Fantasy",
      "file_path": "./uploads/lotr.pdf",
      "audio_path": "./audio/merged_chunk_audio_123.mp3",
      "status": "completed",
      "stream_url": "http://68.183.22.205:8083/user/books/stream/proxy/123",
      "cover_url": "http://68.183.22.205:8083/covers/123_1733097600.jpg",
      "cover_path": "./uploads/covers/123_1733097600.jpg"
    }
  ]
}
```

**Status Values:**
- `pending` - Book created, no file uploaded
- `processing` - File uploaded, being chunked/processed
- `completed` - All audio generation complete
- `failed` - Processing error
- `TTS completed` - Base TTS done (before effects)
- `TTS reused` - Audio reused from duplicate content

**Swift Example:**
```swift
func fetchBooks(category: String? = nil, genre: String? = nil,
                completion: @escaping (Result<[Book], Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401)))
        return
    }

    var urlString = "http://68.183.22.205:8083/user/books"
    var queryItems: [URLQueryItem] = []

    if let category = category {
        queryItems.append(URLQueryItem(name: "category", value: category))
    }
    if let genre = genre {
        queryItems.append(URLQueryItem(name: "genre", value: genre))
    }

    if !queryItems.isEmpty {
        var components = URLComponents(string: urlString)!
        components.queryItems = queryItems
        urlString = components.url!.absoluteString
    }

    var request = URLRequest(url: URL(string: urlString)!)
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    URLSession.shared.dataTask(with: request) { data, response, error in
        // Parse response
    }.resume()
}
```

#### 3. Get Single Book

**Endpoint:** `GET /user/books/:book_id`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 200 OK):**
```json
{
  "book": {
    "id": 123,
    "title": "The Lord of the Rings",
    "author": "J.R.R. Tolkien",
    "category": "Fiction",
    "content": "Full book text content...",
    "content_hash": "abc123def456...",
    "genre": "Fantasy",
    "file_path": "./uploads/lotr.pdf",
    "audio_path": "./audio/merged_chunk_audio_123.mp3",
    "status": "completed"
  }
}
```

#### 4. Delete Book

**Endpoint:** `DELETE /user/books/:book_id`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 200 OK):**
```json
{
  "message": "Book deleted successfully"
}
```

**Response (Error - 404 Not Found):**
```json
{
  "error": "Book not found"
}
```

### Book Search & Discovery

#### Search Books (NEW ✨)

**Endpoint:** `POST /user/search-books`

**Headers:**
```
Authorization: Bearer {token}
Content-Type: application/json
```

**Request:**
```json
{
  "query": "Harry Potter"
}
```

**Response (Success - 200 OK):**
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

**Response (Error - 400 Bad Request):**
```json
{
  "error": "Query parameter is required"
}
```

**What It Does:**
- AI-powered book search using OpenAI GPT-4o with web search
- Returns up to 5 book suggestions with covers and summaries
- Cover URLs from reputable sources (Amazon, Goodreads, OpenLibrary)
- Response time: 5-15 seconds

**Use Cases:**
- Book discovery before creating audiobook
- Autocomplete suggestions while typing
- Browse popular books
- Quick add from search results

**Swift Example:**
```swift
struct BookSearchRequest: Codable {
    let query: String
}

struct BookSuggestion: Codable {
    let title: String
    let author: String
    let cover_url: String
    let summary: String
}

struct BookSearchResponse: Codable {
    let results: [BookSuggestion]
}

func searchBooks(query: String, completion: @escaping (Result<[BookSuggestion], Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401)))
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
        guard let data = data else {
            completion(.failure(error ?? NSError(domain: "NoData", code: 0)))
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
```

**Full Documentation:** See [BOOK_SEARCH_API.md](BOOK_SEARCH_API.md) for complete details, SwiftUI examples, and testing guide.

---

### Playback Progress Tracking

Track where users stop listening so they can resume exactly where they left off, even after closing the app.

#### Update Playback Progress

**Endpoint:** `POST /user/books/:book_id/progress`

**Headers:**
```
Authorization: Bearer {token}
Content-Type: application/json
```

**Request:**
```json
{
  "book_id": 123,
  "current_position": 456.78,
  "duration": 3600.0,
  "chunk_index": 5
}
```

**Parameters:**
- `book_id` (required) - ID of the book
- `current_position` (required) - Current playback position in seconds
- `duration` (optional) - Total duration in seconds (auto-calculated if omitted)
- `chunk_index` (optional) - Current chunk/page index

**Response (Success - 200 OK):**
```json
{
  "book_id": 123,
  "current_position": 456.78,
  "duration": 3600.0,
  "chunk_index": 5,
  "completion_percent": 12.69,
  "last_played_at": "2025-12-15T02:30:00Z"
}
```

**Use Case:**
- Call this endpoint periodically (e.g., every 10-30 seconds) during playback
- Call when user pauses or stops playback
- iOS AVPlayer can report current playback time

**Swift Example:**
```swift
struct UpdateProgressRequest: Codable {
    let book_id: Int
    let current_position: Double
    let duration: Double?
    let chunk_index: Int?
}

func updateProgress(bookID: Int, position: Double, duration: Double? = nil) {
    guard let token = KeychainSwift().get("auth_token") else { return }

    let url = URL(string: "http://68.183.22.205:8083/user/books/\(bookID)/progress")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")

    let body = UpdateProgressRequest(
        book_id: bookID,
        current_position: position,
        duration: duration,
        chunk_index: nil
    )
    request.httpBody = try? JSONEncoder().encode(body)

    URLSession.shared.dataTask(with: request) { data, response, error in
        // Progress saved
    }.resume()
}

// Usage with AVPlayer
func setupProgressTracking(player: AVPlayer, bookID: Int) {
    // Update progress every 15 seconds
    let interval = CMTime(seconds: 15, preferredTimescale: 1)
    player.addPeriodicTimeObserver(forInterval: interval, queue: .main) { time in
        let currentTime = CMTimeGetSeconds(time)
        let duration = CMTimeGetSeconds(player.currentItem?.duration ?? .zero)
        updateProgress(bookID: bookID, position: currentTime, duration: duration)
    }
}
```

---

#### Get Playback Progress

**Endpoint:** `GET /user/books/:book_id/progress`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 200 OK):**
```json
{
  "book_id": 123,
  "current_position": 456.78,
  "duration": 3600.0,
  "chunk_index": 5,
  "completion_percent": 12.69,
  "last_played_at": "2025-12-15T02:30:00Z"
}
```

**Response (No Progress - 200 OK):**
```json
{
  "book_id": 123,
  "current_position": 0,
  "duration": 0,
  "chunk_index": 0,
  "completion_percent": 0,
  "last_played_at": "0001-01-01T00:00:00Z"
}
```

**Use Case:**
- Call when user opens a book to resume from last position
- Display "Resume from X%" in UI

**Swift Example:**
```swift
struct ProgressResponse: Codable {
    let book_id: Int
    let current_position: Double
    let duration: Double
    let chunk_index: Int
    let completion_percent: Double
    let last_played_at: String
}

func getProgress(bookID: Int, completion: @escaping (Result<ProgressResponse, Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401)))
        return
    }

    let url = URL(string: "http://68.183.22.205:8083/user/books/\(bookID)/progress")!
    var request = URLRequest(url: url)
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    URLSession.shared.dataTask(with: request) { data, response, error in
        guard let data = data else {
            completion(.failure(error ?? NSError(domain: "NoData", code: 0)))
            return
        }

        do {
            let progress = try JSONDecoder().decode(ProgressResponse.self, from: data)
            completion(.success(progress))
        } catch {
            completion(.failure(error))
        }
    }.resume()
}

// Usage: Resume playback from saved position
func resumeBook(bookID: Int, player: AVPlayer) {
    getProgress(bookID: bookID) { result in
        switch result {
        case .success(let progress):
            if progress.current_position > 0 {
                let seekTime = CMTime(seconds: progress.current_position, preferredTimescale: 1)
                player.seek(to: seekTime)
                print("Resumed at \(progress.completion_percent)% complete")
            }
        case .failure(let error):
            print("Failed to get progress: \(error)")
        }
    }
}
```

---

#### Get All Progress (Continue Listening)

**Endpoint:** `GET /user/progress`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 200 OK):**
```json
{
  "progress": [
    {
      "book_id": 123,
      "current_position": 456.78,
      "duration": 3600.0,
      "chunk_index": 5,
      "completion_percent": 12.69,
      "last_played_at": "2025-12-15T02:30:00Z"
    },
    {
      "book_id": 124,
      "current_position": 1200.0,
      "duration": 2400.0,
      "chunk_index": 10,
      "completion_percent": 50.0,
      "last_played_at": "2025-12-14T18:00:00Z"
    }
  ],
  "count": 2
}
```

**Use Case:**
- Display "Continue Listening" section with recently played books
- Results are sorted by `last_played_at` (most recent first)
- Show completion percentage for each book

**Swift Example:**
```swift
func getAllProgress(completion: @escaping (Result<[ProgressResponse], Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401)))
        return
    }

    let url = URL(string: "http://68.183.22.205:8083/user/progress")!
    var request = URLRequest(url: url)
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    URLSession.shared.dataTask(with: request) { data, response, error in
        guard let data = data else {
            completion(.failure(error ?? NSError(domain: "NoData", code: 0)))
            return
        }

        do {
            let json = try JSONDecoder().decode([String: AnyCodable].self, from: data)
            if let progressArray = json["progress"]?.value as? [[String: Any]] {
                // Parse progress array
                let progressData = try JSONSerialization.data(withJSONObject: progressArray)
                let progress = try JSONDecoder().decode([ProgressResponse].self, from: progressData)
                completion(.success(progress))
            }
        } catch {
            completion(.failure(error))
        }
    }.resume()
}
```

---

#### Reset Progress (Restart Book)

**Endpoint:** `DELETE /user/books/:book_id/progress`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 200 OK):**
```json
{
  "message": "Progress deleted successfully"
}
```

**Response (Not Found - 404):**
```json
{
  "error": "No progress found for this book"
}
```

**Use Case:**
- User wants to restart book from beginning
- Clear progress when book is deleted

---

### File Upload

#### Upload Book File

**Endpoint:** `POST /user/books/upload`

**Headers:**
```
Authorization: Bearer {token}
Content-Type: multipart/form-data
```

**Form Data:**
- `book_id` - Integer (required)
- `file` - Binary file (required)

**Supported Formats:**
- ✅ PDF (`.pdf`)
- ✅ Plain Text (`.txt`)
- ✅ EPUB (`.epub`)
- ✅ MOBI (`.mobi`)
- ✅ AZW (`.azw`)
- ✅ AZW3 (`.azw3`)
- ❌ KFX (`.kfx`) - Not supported (use Calibre to convert)

**Response (Success - 200 OK):**
```json
{
  "message": "File uploaded and split into pages successfully",
  "book_id": 123,
  "total_pages": 47,
  "file_path": "./uploads/lotr.pdf",
  "content_hash": "abc123def456...",
  "page_indices": 47
}
```

**Response (Error - 400 Bad Request - KFX Format):**
```json
{
  "error": "KFX format is not supported",
  "message": "Please convert your KFX file to EPUB, PDF, MOBI, or AZW3 format first",
  "suggestion": "You can use Calibre or online converters to convert KFX files"
}
```

**What Happens:**
1. File is validated and saved to `./uploads/`
2. SHA256 hash is computed for deduplication
3. Text is extracted (format-specific)
4. Document is chunked into ~1000 character pages
5. Each page is saved as a `BookChunk` record
6. Book status is updated to "processing"

**Swift Example:**
```swift
func uploadBookFile(bookId: Int, fileURL: URL,
                    completion: @escaping (Result<UploadResponse, Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401)))
        return
    }

    let url = URL(string: "http://68.183.22.205:8083/user/books/upload")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    let boundary = "Boundary-\(UUID().uuidString)"
    request.setValue("multipart/form-data; boundary=\(boundary)",
                     forHTTPHeaderField: "Content-Type")

    var body = Data()

    // Add book_id
    body.append("--\(boundary)\r\n".data(using: .utf8)!)
    body.append("Content-Disposition: form-data; name=\"book_id\"\r\n\r\n".data(using: .utf8)!)
    body.append("\(bookId)\r\n".data(using: .utf8)!)

    // Add file
    body.append("--\(boundary)\r\n".data(using: .utf8)!)
    body.append("Content-Disposition: form-data; name=\"file\"; filename=\"\(fileURL.lastPathComponent)\"\r\n".data(using: .utf8)!)
    body.append("Content-Type: application/octet-stream\r\n\r\n".data(using: .utf8)!)

    if let fileData = try? Data(contentsOf: fileURL) {
        body.append(fileData)
    }

    body.append("\r\n--\(boundary)--\r\n".data(using: .utf8)!)

    request.httpBody = body

    URLSession.shared.dataTask(with: request) { data, response, error in
        // Parse response
    }.resume()
}
```

### Book Pages (Chunks)

#### List Book Pages

**Endpoint:** `GET /user/books/:book_id/chunks/pages`

**Headers:**
```
Authorization: Bearer {token}
```

**Query Parameters (Optional):**
- `limit` - Number of pages to return (default: 20)
- `offset` - Number of pages to skip (default: 0)

**Examples:**
- `GET /user/books/123/chunks/pages` - Get first 20 pages
- `GET /user/books/123/chunks/pages?limit=50` - Get first 50 pages
- `GET /user/books/123/chunks/pages?limit=20&offset=40` - Get pages 41-60

**Response (Success - 200 OK):**
```json
{
  "book_id": 123,
  "title": "The Lord of the Rings",
  "status": "completed",
  "total_pages": 47,
  "limit": 20,
  "offset": 0,
  "fully_processed": true,
  "pages": [
    {
      "page": 1,
      "content": "In a hole in the ground there lived a hobbit...",
      "status": "completed",
      "audio_url": "http://68.183.22.205:8083/user/books/123/pages/0/audio"
    },
    {
      "page": 2,
      "content": "Not a nasty, dirty, wet hole...",
      "status": "completed",
      "audio_url": "http://68.183.22.205:8083/user/books/123/pages/1/audio"
    }
  ]
}
```

**Page Status Values:**
- `pending` - Page not yet processed
- `processing` - TTS in progress
- `completed` - Audio ready
- `failed` - Processing error

**Use Case:** Display book pages to user, show progress, allow page-by-page playback

### Text-to-Speech (TTS)

#### Process Specific Pages (1-2 pages)

**Endpoint:** `POST /user/chunks/tts`

**Headers:**
```
Authorization: Bearer {token}
Content-Type: application/json
```

**Request:**
```json
{
  "book_id": 123,
  "pages": [1, 2]
}
```

**Constraints:**
- Must specify 1 or 2 pages only
- Pages are 1-based (page 1 = index 0)
- Free accounts: Max 1 completed page total across all books
- Paid accounts: Unlimited

**Response (Success - 200 OK):**
```json
{
  "message": "TTS processing complete",
  "audio_paths": [
    "./audio/audio_456.mp3",
    "./audio/audio_457.mp3"
  ]
}
```

**Response (Error - 403 Forbidden - Free Limit):**
```json
{
  "error": "Free trial limit reached. Upgrade your plan to continue transcribing."
}
```

**Response (Error - 400 Bad Request - Too Many Pages):**
```json
{
  "error": "You must provide 1 or 2 pages to process"
}
```

**What Happens:**
1. Pages are converted to TTS audio (OpenAI)
2. Background music is generated (ElevenLabs)
3. Audio is merged with music
4. Foley sound effects are overlayed
5. `final_audio_path` is saved for each page

**Processing Time:** ~30-60 seconds per page

#### Batch Process Entire Book

**Endpoint:** `POST /user/books/:book_id/tts/batch`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 202 Accepted):**
```json
{
  "message": "Batch transcription started in background"
}
```

**What Happens:**
1. All unprocessed pages are queued
2. Each page is processed sequentially:
   - Text → SSML (GPT-4)
   - SSML → MP3 (OpenAI TTS)
   - Generate background music (ElevenLabs)
   - Merge audio + music
   - Overlay sound effects
3. Book status updated to "completed" when done

**Processing Time:** ~1-2 minutes per page (depends on book length)

**Important:** This is a **background job** - response is immediate. Poll `/user/books/:book_id/chunks/pages` to check progress.

---

## Audio Streaming

### iOS AVPlayer Requirements

**Critical:** iOS AVPlayer requires the authentication token to be passed in the **URL query parameter** (not header).

### 1. Stream Single Page Audio

**Endpoint:** `GET /user/books/:book_id/pages/:page/audio`

**Query Parameters:**
- `token` - JWT token (required for iOS AVPlayer)

**Example:**
```
GET /user/books/123/pages/0/audio?token={jwt_token}
```

**Response:**
- **Content-Type:** `audio/mpeg`
- **Body:** MP3 audio stream with background music and effects

**Swift Example (AVPlayer):**
```swift
import AVFoundation

func playPage(bookId: Int, pageIndex: Int) {
    guard let token = KeychainSwift().get("auth_token") else { return }

    // IMPORTANT: Token in URL query for AVPlayer
    let urlString = "http://68.183.22.205:8083/user/books/\(bookId)/pages/\(pageIndex)/audio?token=\(token)"

    guard let url = URL(string: urlString) else { return }

    let playerItem = AVPlayerItem(url: url)
    let player = AVPlayer(playerItem: playerItem)
    player.play()
}
```

### 2. Stream Entire Book

**Endpoint:** `GET /user/books/stream/proxy/:book_id`

**Query Parameters:**
- `token` - JWT token (required)

**Example:**
```
GET /user/books/stream/proxy/123?token={jwt_token}
```

**Response:**
- **Content-Type:** `audio/mpeg`
- **Body:** Complete merged audio for the book

**Access Control:**
- Token is validated
- User can only stream their own books
- Returns 403 Forbidden if trying to access another user's book

### 3. Stream Merged Chunk Audio

**Endpoint:** `GET /user/chunks/tts/merged-audio/:book_id`

**Query Parameters:**
- `token` - JWT token

**Response:**
- Latest merged audio file for the book
- Pattern: `./audio/merged_chunk_audio_{book_id}*.mp3`

### 4. Stream Chunk Range

**Endpoint:** `GET /user/books/:book_id/chunks/:start/:end/audio`

**Query Parameters:**
- `token` - JWT token

**Example:**
```
GET /user/books/123/chunks/0/9/audio?token={jwt_token}
```

**Response:**
- Merged audio from chunk index `start` to `end` (inclusive)

### 5. Stream by Chunk IDs

**Endpoint:** `POST /user/chunks/audio-by-id`

**Headers:**
```
Authorization: Bearer {token}
Content-Type: application/json
```

**Request:**
```json
{
  "chunk_ids": [456, 457, 458]
}
```

**Response:**
- Merged audio for specified chunk IDs

---

## Book Covers

### Automatic Cover Fetching (Default)

When you create a book, the backend **automatically**:
1. Searches the web for the book cover using OpenAI
2. Downloads the image (target: 1000×1600px, 0.625 aspect ratio)
3. Saves to `./uploads/covers/`
4. Updates book with `cover_url` and `cover_path`
5. Publishes MQTT event to `users/{user_id}/cover_uploaded`

**Timeline:** 5-15 seconds (background process)

**MQTT Event:**
```json
{
  "book_id": 123,
  "cover_url": "http://68.183.22.205:8083/covers/123_1733097600.jpg",
  "timestamp": "2025-12-12T10:00:15Z",
  "source": "web_search"
}
```

**iOS Implementation:**
```swift
// Option 1: Subscribe to MQTT (recommended)
func monitorCoverFetch(bookId: Int, userId: Int) {
    MQTTManager.shared.subscribe(to: "users/\(userId)/cover_uploaded")

    MQTTManager.shared.onCoverUploaded = { event in
        if event.book_id == bookId && event.source == "web_search" {
            DispatchQueue.main.async {
                // Update UI with event.cover_url
                self.bookCoverURL = event.cover_url
            }
        }
    }
}

// Option 2: Poll book details (fallback)
func pollForCover(bookId: Int) {
    Timer.scheduledTimer(withTimeInterval: 2.0, repeats: true) { timer in
        self.fetchBook(id: bookId) { book in
            if let coverURL = book.cover_url, !coverURL.isEmpty {
                timer.invalidate()
                // Cover is ready
                self.bookCoverURL = coverURL
            }
        }
    }
}
```

### Manual Cover Upload (Fallback)

**Endpoint:** `POST /user/books/:book_id/cover`

**Headers:**
```
Authorization: Bearer {token}
Content-Type: multipart/form-data
```

**Form Data:**
- `cover` - Image file (JPG, JPEG, PNG)

**Response (Success - 202 Accepted):**
```json
{
  "message": "upload in progress",
  "cover_url": "http://68.183.22.205:8083/covers/123_1733097600.jpg"
}
```

**MQTT Event:**
```json
{
  "book_id": 123,
  "cover_url": "http://68.183.22.205:8083/covers/123_1733097600.jpg",
  "timestamp": "2025-12-12T10:05:00Z",
  "source": "manual_upload"
}
```

**When to Use:**
- Automatic fetch failed
- User wants custom cover
- Book is obscure/not found online

---

## Stripe Subscription

### Create Checkout Session

**Endpoint:** `POST /user/stripe/create-checkout-session`

**Headers:**
```
Authorization: Bearer {token}
```

**Response (Success - 200 OK):**
```json
{
  "url": "https://checkout.stripe.com/pay/cs_test_..."
}
```

**Usage:**
1. iOS app calls this endpoint
2. Opens returned URL in Safari/SFSafariViewController
3. User completes payment on Stripe-hosted page
4. Stripe webhook updates user account to "paid"
5. User is redirected to success/cancel page

**Swift Example:**
```swift
import SafariServices

func startSubscription() {
    guard let token = KeychainSwift().get("auth_token") else { return }

    let url = URL(string: "http://68.183.22.205:8080/user/stripe/create-checkout-session")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    URLSession.shared.dataTask(with: request) { data, response, error in
        guard let data = data,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let checkoutURL = json["url"] as? String,
              let url = URL(string: checkoutURL) else { return }

        DispatchQueue.main.async {
            let safariVC = SFSafariViewController(url: url)
            self.present(safariVC, animated: true)
        }
    }.resume()
}
```

### Subscription Tiers

#### Free Account
- 1 completed page maximum (across all books)
- Can create unlimited books
- Can upload unlimited files
- Limited to 1 page TTS

#### Paid Account
- Unlimited pages
- Unlimited books
- All features unlocked

**Check Before Processing:**
```swift
func checkCanProcess() {
    APIClient.shared.get("/user/account-type") { result in
        switch result {
        case .success(let data):
            if let accountType = data["account_type"] as? String {
                if accountType == "free" {
                    // Check if user has reached limit
                    self.showUpgradePrompt()
                } else {
                    // Proceed with processing
                }
            }
        case .failure:
            break
        }
    }
}
```

---

## MQTT Real-Time Events

### Connection Setup

**Broker URL:** Set via `MQTT_BROKER` env variable
**Production:** `tcp://10.116.0.8:1883` (internal VPC)

**iOS Implementation (CocoaMQTT):**
```swift
import CocoaMQTT

class MQTTManager: ObservableObject {
    static let shared = MQTTManager()

    private var mqttClient: CocoaMQTT?
    var onCoverUploaded: ((CoverEvent) -> Void)?

    func connect(userId: Int) {
        let clientID = "ios-\(userId)-\(Date().timeIntervalSince1970)"

        mqttClient = CocoaMQTT(
            clientID: clientID,
            host: "68.183.22.205",  // Production gateway
            port: 1883
        )

        mqttClient?.username = "optional_username"
        mqttClient?.password = "optional_password"
        mqttClient?.keepAlive = 60
        mqttClient?.delegate = self

        _ = mqttClient?.connect()
    }

    func subscribe(to topic: String) {
        mqttClient?.subscribe(topic, qos: .qos1)
    }

    func disconnect() {
        mqttClient?.disconnect()
    }
}

extension MQTTManager: CocoaMQTTDelegate {
    func mqtt(_ mqtt: CocoaMQTT, didConnectAck ack: CocoaMQTTConnAck) {
        if ack == .accept {
            print("✅ MQTT Connected")
        }
    }

    func mqtt(_ mqtt: CocoaMQTT, didReceiveMessage message: CocoaMQTTMessage, id: UInt16) {
        guard let payloadString = message.string else { return }

        if message.topic.contains("cover_uploaded") {
            handleCoverEvent(payloadString)
        }
    }

    private func handleCoverEvent(_ json: String) {
        guard let data = json.data(using: .utf8),
              let event = try? JSONDecoder().decode(CoverEvent.self, from: data) else {
            return
        }

        DispatchQueue.main.async {
            self.onCoverUploaded?(event)
        }
    }
}

struct CoverEvent: Codable {
    let book_id: Int
    let cover_url: String
    let timestamp: String
    let source: String  // "web_search" or "manual_upload"
}
```

### Topics

#### Cover Upload Events

**Topic:** `users/{user_id}/cover_uploaded`

**Payload:**
```json
{
  "book_id": 123,
  "cover_url": "http://68.183.22.205:8083/covers/123_1733097600.jpg",
  "timestamp": "2025-12-12T10:00:15Z",
  "source": "web_search"
}
```

**Subscribe:**
```swift
MQTTManager.shared.subscribe(to: "users/42/cover_uploaded")
```

#### Debug Events

**Topic:** `debug/ping`

**Usage:** Test MQTT connectivity

---

## Data Models

### Complete Swift Models

```swift
import Foundation

// MARK: - Authentication

struct SignupRequest: Codable {
    let username: String
    let email: String
    let password: String
    let state: String
}

struct LoginRequest: Codable {
    let username: String
    let password: String
}

struct LoginResponse: Codable {
    let token: String
}

struct UserProfile: Codable {
    let username: String
    let email: String
    let account_type: String
    let is_public: Bool
    let state: String
    let books_read: Int
    let created_at: String

    enum CodingKeys: String, CodingKey {
        case username, email, state
        case account_type = "account_type"
        case is_public = "is_public"
        case books_read = "books_read"
        case created_at = "created_at"
    }
}

// MARK: - Books

struct Book: Codable, Identifiable {
    let ID: Int
    let Title: String
    let Author: String
    let Category: String
    let Genre: String?
    let Content: String?
    let ContentHash: String?
    let FilePath: String?
    let AudioPath: String?
    let Status: String
    let UserID: Int?
    let CoverPath: String?
    let CoverURL: String?
    let Index: Int?
    let CreatedAt: String?
    let UpdatedAt: String?

    var id: Int { ID }

    // Convenience properties
    var isProcessed: Bool {
        Status == "completed"
    }

    var hasCover: Bool {
        CoverURL != nil && !CoverURL!.isEmpty
    }

    var hasAudio: Bool {
        AudioPath != nil && !AudioPath!.isEmpty
    }
}

struct BookCreateRequest: Codable {
    let title: String
    let author: String
    let category: String
    let genre: String?
}

struct BookListResponse: Codable {
    let books: [BookResponse]
}

struct BookResponse: Codable {
    let id: Int
    let title: String
    let author: String
    let category: String
    let genre: String?
    let file_path: String?
    let audio_path: String?
    let status: String
    let stream_url: String?
    let cover_url: String?
    let cover_path: String?
}

// MARK: - Book Pages

struct BookPagesResponse: Codable {
    let book_id: Int
    let title: String
    let status: String
    let total_pages: Int
    let limit: Int
    let offset: Int
    let fully_processed: Bool
    let pages: [BookPage]
}

struct BookPage: Codable, Identifiable {
    let page: Int
    let content: String
    let status: String
    let audio_url: String

    var id: Int { page }

    var isProcessed: Bool {
        status == "completed"
    }
}

// MARK: - Upload

struct UploadResponse: Codable {
    let message: String
    let book_id: Int
    let total_pages: Int
    let file_path: String
    let content_hash: String
    let page_indices: Int
}

// MARK: - TTS

struct TTSRequest: Codable {
    let book_id: Int
    let pages: [Int]
}

struct TTSResponse: Codable {
    let message: String
    let audio_paths: [String]
}

// MARK: - MQTT Events

struct CoverEvent: Codable {
    let book_id: Int
    let cover_url: String
    let timestamp: String
    let source: String  // "web_search" or "manual_upload"
}

// MARK: - Stripe

struct CheckoutSessionResponse: Codable {
    let url: String
}

struct AccountTypeResponse: Codable {
    let account_type: String
}
```

---

## Error Handling

### HTTP Status Codes

| Code | Meaning | Common Causes |
|------|---------|---------------|
| 200 | OK | Success |
| 202 | Accepted | Background job started |
| 400 | Bad Request | Invalid input, validation error |
| 401 | Unauthorized | Missing/invalid token |
| 403 | Forbidden | Free account limit, permission denied |
| 404 | Not Found | Resource doesn't exist |
| 500 | Internal Server Error | Backend error |

### Error Response Format

```json
{
  "error": "Error message here",
  "details": "Optional additional info"
}
```

### Swift Error Handling

```swift
enum APIError: Error {
    case invalidURL
    case noData
    case decodingError
    case unauthorized
    case forbidden(String)
    case notFound
    case serverError(String)
    case unknown(Int)

    init(statusCode: Int, data: Data?) {
        switch statusCode {
        case 401:
            self = .unauthorized
        case 403:
            if let data = data,
               let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let message = json["error"] as? String {
                self = .forbidden(message)
            } else {
                self = .forbidden("Access denied")
            }
        case 404:
            self = .notFound
        case 500...599:
            if let data = data,
               let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let message = json["error"] as? String {
                self = .serverError(message)
            } else {
                self = .serverError("Server error")
            }
        default:
            self = .unknown(statusCode)
        }
    }
}

// Usage in network calls
URLSession.shared.dataTask(with: request) { data, response, error in
    if let error = error {
        completion(.failure(error))
        return
    }

    guard let httpResponse = response as? HTTPURLResponse else {
        completion(.failure(APIError.noData))
        return
    }

    guard 200...299 ~= httpResponse.statusCode else {
        let apiError = APIError(statusCode: httpResponse.statusCode, data: data)
        completion(.failure(apiError))
        return
    }

    // Handle success
}.resume()
```

### Common Error Scenarios

#### 1. Token Expired (401)
```swift
func handleTokenExpired() {
    // Clear stored token
    KeychainSwift().delete("auth_token")

    // Show login screen
    DispatchQueue.main.async {
        self.showLogin()
    }
}
```

#### 2. Free Account Limit (403)
```swift
func handleFreeLimit(message: String) {
    let alert = UIAlertController(
        title: "Upgrade Required",
        message: message,
        preferredStyle: .alert
    )

    alert.addAction(UIAlertAction(title: "Upgrade", style: .default) { _ in
        self.startSubscription()
    })

    alert.addAction(UIAlertAction(title: "Cancel", style: .cancel))

    present(alert, animated: true)
}
```

#### 3. Network Timeout
```swift
func configureURLSession() -> URLSession {
    let config = URLSessionConfiguration.default
    config.timeoutIntervalForRequest = 30
    config.timeoutIntervalForResource = 300
    return URLSession(configuration: config)
}
```

---

## Core Workflows

### Complete Audiobook Creation Flow

```swift
class AudiobookCreationManager: ObservableObject {
    @Published var currentStep: CreationStep = .bookInfo
    @Published var book: Book?
    @Published var uploadProgress: Double = 0
    @Published var processingStatus: String = ""

    enum CreationStep {
        case bookInfo
        case fileUpload
        case coverFetching
        case audioProcessing
        case complete
    }

    // Step 1: Create Book
    func createBook(title: String, author: String, category: String, genre: String?) {
        APIClient.shared.createBook(title: title, author: author,
                                   category: category, genre: genre) { [weak self] result in
            switch result {
            case .success(let book):
                self?.book = book
                self?.currentStep = .fileUpload

                // Start monitoring cover fetch
                if let userId = AuthManager.shared.currentUserId {
                    self?.monitorCoverFetch(bookId: book.ID, userId: userId)
                }

            case .failure(let error):
                self?.handleError(error)
            }
        }
    }

    // Step 2: Upload File
    func uploadFile(fileURL: URL) {
        guard let bookId = book?.ID else { return }

        APIClient.shared.uploadBookFile(bookId: bookId, fileURL: fileURL,
                                       progressHandler: { progress in
            DispatchQueue.main.async {
                self.uploadProgress = progress
            }
        }) { [weak self] result in
            switch result {
            case .success(let response):
                self?.processingStatus = "File uploaded: \(response.total_pages) pages"
                self?.currentStep = .audioProcessing

                // Start batch processing
                self?.startBatchProcessing(bookId: bookId)

            case .failure(let error):
                self?.handleError(error)
            }
        }
    }

    // Step 3: Monitor Cover Fetch (Parallel)
    func monitorCoverFetch(bookId: Int, userId: Int) {
        currentStep = .coverFetching

        MQTTManager.shared.subscribe(to: "users/\(userId)/cover_uploaded")
        MQTTManager.shared.onCoverUploaded = { [weak self] event in
            if event.book_id == bookId {
                DispatchQueue.main.async {
                    self?.book?.CoverURL = event.cover_url
                    self?.processingStatus = "Cover ready!"
                }
            }
        }
    }

    // Step 4: Start Batch Audio Processing
    func startBatchProcessing(bookId: Int) {
        APIClient.shared.batchProcessBook(bookId: bookId) { [weak self] result in
            switch result {
            case .success:
                // Start polling for progress
                self?.pollProcessingProgress(bookId: bookId)

            case .failure(let error):
                self?.handleError(error)
            }
        }
    }

    // Step 5: Poll Processing Progress
    func pollProcessingProgress(bookId: Int) {
        Timer.scheduledTimer(withTimeInterval: 5.0, repeats: true) { [weak self] timer in
            self?.checkProgress(bookId: bookId) { completed, total in
                let progress = Double(completed) / Double(total)
                self?.processingStatus = "Processing: \(completed)/\(total) pages"

                if completed == total {
                    timer.invalidate()
                    self?.currentStep = .complete
                    self?.processingStatus = "Audiobook ready!"
                }
            }
        }
    }

    func checkProgress(bookId: Int, completion: @escaping (Int, Int) -> Void) {
        APIClient.shared.fetchBookPages(bookId: bookId) { result in
            switch result {
            case .success(let response):
                let completed = response.pages.filter { $0.status == "completed" }.count
                completion(completed, response.total_pages)

            case .failure:
                break
            }
        }
    }
}
```

### Usage in SwiftUI:
```swift
struct CreateAudiobookView: View {
    @StateObject private var manager = AudiobookCreationManager()

    var body: some View {
        VStack {
            switch manager.currentStep {
            case .bookInfo:
                BookInfoForm(onSubmit: manager.createBook)

            case .fileUpload:
                FileUploadView(onUpload: manager.uploadFile)

            case .coverFetching:
                ProgressView("Fetching book cover...")

            case .audioProcessing:
                VStack {
                    ProgressView(value: manager.uploadProgress)
                    Text(manager.processingStatus)
                }

            case .complete:
                CompletionView(book: manager.book)
            }
        }
    }
}
```

---

## Best Practices

### 1. Token Management

**Do:**
- ✅ Store tokens in Keychain (never UserDefaults)
- ✅ Refresh token on 401 errors
- ✅ Clear token on logout

**Don't:**
- ❌ Store tokens in UserDefaults
- ❌ Log tokens to console in production
- ❌ Hardcode tokens

```swift
class TokenManager {
    private let keychain = KeychainSwift()
    private let tokenKey = "auth_token"

    func saveToken(_ token: String) {
        keychain.set(token, forKey: tokenKey)
    }

    func getToken() -> String? {
        return keychain.get(tokenKey)
    }

    func clearToken() {
        keychain.delete(tokenKey)
    }
}
```

### 2. Audio Streaming

**For AVPlayer:**
```swift
func streamAudio(bookId: Int, pageIndex: Int) {
    guard let token = TokenManager().getToken() else { return }

    // CRITICAL: Token in URL query for AVPlayer
    let urlString = "http://68.183.22.205:8083/user/books/\(bookId)/pages/\(pageIndex)/audio?token=\(token)"

    guard let url = URL(string: urlString) else { return }

    let playerItem = AVPlayerItem(url: url)

    // Add observer for playback status
    playerItem.addObserver(self, forKeyPath: "status",
                          options: [.new, .old], context: nil)

    let player = AVPlayer(playerItem: playerItem)
    player.play()
}

override func observeValue(forKeyPath keyPath: String?, of object: Any?,
                          change: [NSKeyValueChangeKey : Any]?, context: UnsafeMutableRawPointer?) {
    if keyPath == "status" {
        if let playerItem = object as? AVPlayerItem {
            switch playerItem.status {
            case .readyToPlay:
                print("Ready to play")
            case .failed:
                print("Failed to load: \(playerItem.error?.localizedDescription ?? "Unknown")")
            case .unknown:
                print("Unknown status")
            @unknown default:
                break
            }
        }
    }
}
```

### 3. Network Optimization

**Caching:**
```swift
func configureCaching() -> URLSessionConfiguration {
    let config = URLSessionConfiguration.default

    // 50MB memory cache, 100MB disk cache
    let cache = URLCache(memoryCapacity: 50 * 1024 * 1024,
                        diskCapacity: 100 * 1024 * 1024,
                        diskPath: "audio_cache")
    config.urlCache = cache
    config.requestCachePolicy = .returnCacheDataElseLoad

    return config
}
```

**Background Downloads:**
```swift
func downloadBookAudio(bookId: Int) {
    let config = URLSessionConfiguration.background(withIdentifier: "com.app.audio")
    let session = URLSession(configuration: config, delegate: self, delegateQueue: nil)

    guard let token = TokenManager().getToken() else { return }
    let urlString = "http://68.183.22.205:8083/user/books/stream/proxy/\(bookId)?token=\(token)"

    if let url = URL(string: urlString) {
        let task = session.downloadTask(with: url)
        task.resume()
    }
}
```

### 4. MQTT Connection Management

```swift
class MQTTManager {
    func connect() {
        // Connect on app foreground
        NotificationCenter.default.addObserver(
            self,
            selector: #selector(appDidBecomeActive),
            name: UIApplication.didBecomeActiveNotification,
            object: nil
        )

        // Disconnect on background
        NotificationCenter.default.addObserver(
            self,
            selector: #selector(appDidEnterBackground),
            name: UIApplication.didEnterBackgroundNotification,
            object: nil
        )
    }

    @objc func appDidBecomeActive() {
        mqttClient?.connect()
    }

    @objc func appDidEnterBackground() {
        mqttClient?.disconnect()
    }
}
```

### 5. Error Recovery

```swift
func retryOperation<T>(maxAttempts: Int = 3,
                      delay: TimeInterval = 2.0,
                      operation: @escaping (@escaping (Result<T, Error>) -> Void) -> Void,
                      completion: @escaping (Result<T, Error>) -> Void) {
    var attempts = 0

    func attempt() {
        attempts += 1

        operation { result in
            switch result {
            case .success:
                completion(result)

            case .failure(let error):
                if attempts < maxAttempts {
                    DispatchQueue.main.asyncAfter(deadline: .now() + delay) {
                        attempt()
                    }
                } else {
                    completion(.failure(error))
                }
            }
        }
    }

    attempt()
}

// Usage
retryOperation(maxAttempts: 3) { completion in
    APIClient.shared.fetchBooks { result in
        completion(result)
    }
} completion: { result in
    // Handle final result
}
```

---

## Testing Guide

### Unit Test Examples

```swift
import XCTest
@testable import YourApp

class AuthenticationTests: XCTestCase {
    func testLoginSuccess() {
        let expectation = XCTestExpectation(description: "Login successful")

        APIClient.shared.login(username: "testuser", password: "password") { result in
            switch result {
            case .success(let token):
                XCTAssertFalse(token.isEmpty)
                expectation.fulfill()

            case .failure:
                XCTFail("Login should succeed")
            }
        }

        wait(for: [expectation], timeout: 10.0)
    }

    func testLoginInvalidCredentials() {
        let expectation = XCTestExpectation(description: "Login fails with invalid credentials")

        APIClient.shared.login(username: "invalid", password: "wrong") { result in
            switch result {
            case .success:
                XCTFail("Login should fail")

            case .failure(let error):
                XCTAssertEqual((error as? APIError), .unauthorized)
                expectation.fulfill()
            }
        }

        wait(for: [expectation], timeout: 10.0)
    }
}

class BookManagementTests: XCTestCase {
    func testCreateBook() {
        let expectation = XCTestExpectation(description: "Book created")

        APIClient.shared.createBook(title: "Test Book", author: "Test Author",
                                   category: "Fiction", genre: "Mystery") { result in
            switch result {
            case .success(let book):
                XCTAssertEqual(book.Title, "Test Book")
                XCTAssertEqual(book.Status, "pending")
                expectation.fulfill()

            case .failure:
                XCTFail("Book creation should succeed")
            }
        }

        wait(for: [expectation], timeout: 10.0)
    }
}
```

### Integration Test Checklist

- [ ] **Authentication Flow**
  - [ ] Signup with valid data
  - [ ] Signup with duplicate email (should fail)
  - [ ] Login with valid credentials
  - [ ] Login with invalid credentials (should fail)
  - [ ] Access protected endpoint with valid token
  - [ ] Access protected endpoint with expired token (should fail)

- [ ] **Book Management**
  - [ ] Create book with all fields
  - [ ] Create book with minimal fields
  - [ ] Create book with invalid category (should fail)
  - [ ] List books (empty)
  - [ ] List books (with data)
  - [ ] Filter books by category
  - [ ] Get single book by ID
  - [ ] Delete book

- [ ] **File Upload**
  - [ ] Upload PDF file
  - [ ] Upload TXT file
  - [ ] Upload EPUB file
  - [ ] Upload MOBI file
  - [ ] Upload KFX file (should fail with helpful message)
  - [ ] Upload without book_id (should fail)

- [ ] **Audio Processing**
  - [ ] Process single page (free account)
  - [ ] Process 2 pages at once
  - [ ] Process 3 pages (should fail - max 2)
  - [ ] Batch process entire book
  - [ ] Exceed free account limit (should fail)
  - [ ] Process with paid account

- [ ] **Audio Streaming**
  - [ ] Stream single page with valid token
  - [ ] Stream entire book
  - [ ] Stream without token (should fail)
  - [ ] Stream with expired token (should fail)
  - [ ] Stream another user's book (should fail)
  - [ ] AVPlayer playback (token in URL)

- [ ] **Book Covers**
  - [ ] Automatic cover fetch (well-known book)
  - [ ] MQTT event received when cover ready
  - [ ] Manual cover upload
  - [ ] Poll for cover status

- [ ] **Subscription**
  - [ ] Create checkout session
  - [ ] Complete payment (test mode)
  - [ ] Account upgraded to "paid"
  - [ ] Cancel subscription
  - [ ] Account downgraded to "free"

- [ ] **MQTT**
  - [ ] Connect to broker
  - [ ] Subscribe to cover events
  - [ ] Receive event when cover uploaded
  - [ ] Disconnect gracefully

### Manual Testing Scenarios

#### Scenario 1: Complete Audiobook Creation
1. Sign up new user
2. Create book ("Harry Potter and the Philosopher's Stone", "J.K. Rowling", "Fiction", "Fantasy")
3. Wait 10 seconds - verify cover appears
4. Upload PDF file
5. Start batch processing
6. Poll every 5 seconds until all pages complete
7. Stream first page audio
8. Verify audio plays with background music

#### Scenario 2: Free Account Limits
1. Create free account
2. Create book and upload file
3. Process 1 page - should succeed
4. Try to process another page - should fail with upgrade message
5. Create Stripe checkout session
6. Complete payment (test mode)
7. Retry processing - should succeed

#### Scenario 3: Error Handling
1. Login with expired token - should redirect to login
2. Upload file for non-existent book - should show error
3. Stream audio while offline - should handle gracefully
4. Try to delete another user's book - should fail

---

## Additional Resources

### Environment Variables Reference

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

# APIs
OPENAI_API_KEY=<set in deploy>
XI_API_KEY=<set in deploy>

# Stripe
STRIPE_SECRET_KEY=<set in deploy>
STRIPE_WEBHOOK_SECRET=<set in deploy>

# MQTT
MQTT_BROKER=tcp://10.116.0.8:1883
MQTT_USERNAME=<optional>
MQTT_PASSWORD=<optional>

# Deployment
STREAM_HOST=http://68.183.22.205:8083
```

### Useful Curl Commands

```bash
# Login
curl -X POST http://68.183.22.205:8080/login \
  -H "Content-Type: application/json" \
  -d '{"username": "testuser", "password": "password"}'

# Create Book
curl -X POST http://68.183.22.205:8083/user/books \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{"title": "Test Book", "author": "Author", "category": "Fiction"}'

# List Books
curl -X GET http://68.183.22.205:8083/user/books \
  -H "Authorization: Bearer {token}"

# Upload File
curl -X POST http://68.183.22.205:8083/user/books/upload \
  -H "Authorization: Bearer {token}" \
  -F "book_id=123" \
  -F "file=@/path/to/book.pdf"

# Stream Audio (download to file)
curl -X GET "http://68.183.22.205:8083/user/books/stream/proxy/123?token={token}" \
  --output audiobook.mp3
```

### Support File Formats

| Format | Extension | Support | Text Extraction | Notes |
|--------|-----------|---------|----------------|-------|
| PDF | `.pdf` | ✅ Yes | rsc.io/pdf | Works with most PDFs |
| Plain Text | `.txt` | ✅ Yes | Direct read | UTF-8 encoding |
| EPUB | `.epub` | ✅ Yes | ZIP extraction | Extracts from XHTML/HTML |
| MOBI | `.mobi` | ✅ Yes | Calibre | Requires ebook-convert |
| AZW | `.azw` | ✅ Yes | Calibre | Amazon format |
| AZW3 | `.azw3` | ✅ Yes | Calibre | Amazon format |
| KFX | `.kfx` | ❌ No | N/A | Use Calibre to convert |

### Free vs Paid Account Comparison

| Feature | Free | Paid |
|---------|------|------|
| Create Books | ✅ Unlimited | ✅ Unlimited |
| Upload Files | ✅ Unlimited | ✅ Unlimited |
| Automatic Cover Fetch | ✅ Yes | ✅ Yes |
| TTS Processing | ⚠️ 1 page max | ✅ Unlimited |
| Audio Streaming | ✅ Yes | ✅ Yes |
| MQTT Events | ✅ Yes | ✅ Yes |
| Background Music | ⚠️ Limited | ✅ Yes |
| Sound Effects | ⚠️ Limited | ✅ Yes |

---

## Quick Start Checklist

For iOS developers starting integration:

- [ ] **Setup**
  - [ ] Add dependencies: `Alamofire`, `KeychainSwift`, `CocoaMQTT`
  - [ ] Configure base URL: `http://68.183.22.205:8080`
  - [ ] Create API client singleton

- [ ] **Authentication**
  - [ ] Implement signup screen
  - [ ] Implement login screen
  - [ ] Store JWT in Keychain
  - [ ] Add token to all API requests

- [ ] **Book Management**
  - [ ] Implement book creation form
  - [ ] Implement file upload (document picker)
  - [ ] Display book list
  - [ ] Show book details

- [ ] **Audio Features**
  - [ ] Integrate AVPlayer for streaming
  - [ ] Add token to streaming URLs
  - [ ] Implement playback controls
  - [ ] Show page-by-page progress

- [ ] **Real-Time Updates**
  - [ ] Setup MQTT connection
  - [ ] Subscribe to cover events
  - [ ] Update UI when events received

- [ ] **Subscription**
  - [ ] Integrate Stripe checkout
  - [ ] Handle upgrade flow
  - [ ] Show account type in UI

---

## Conclusion

This guide provides comprehensive coverage of the Stream Audio microservice API for iOS integration. The backend is production-ready with:

- ✅ Robust authentication (JWT with 72-hour expiry)
- ✅ Multi-format document support (PDF, TXT, EPUB, MOBI, AZW, AZW3)
- ✅ AI-powered audio generation (TTS + music + effects)
- ✅ Automatic book cover fetching
- ✅ Real-time MQTT notifications
- ✅ Stripe subscription management
- ✅ Audio streaming optimized for iOS AVPlayer

For questions or issues, refer to the [CLAUDE.md](claude.md) for backend architecture details or check the production logs:

```bash
docker compose -f docker-compose.prod.yml logs -f content-service
docker compose -f docker-compose.prod.yml logs -f auth-service
docker compose -f docker-compose.prod.yml logs -f gateway
```

**Production URL:** `http://68.183.22.205:8080`
**Backend Version:** 1.0
**Last Updated:** December 12, 2025
