# Stream Audio Frontend API Architecture

> **Purpose**: This document provides a complete API reference for building a TypeScript/HTML/CSS frontend for the Stream Audio platform. It covers authentication, book management, file uploads, TTS processing, and audio streaming.

---

## Table of Contents

1. [Overview](#overview)
2. [Authentication](#authentication)
3. [TypeScript Types](#typescript-types)
4. [API Endpoints](#api-endpoints)
   - [Auth & User Management](#auth--user-management)
   - [Social Login](#social-login) ✨ NEW
   - [Book Management](#book-management)
   - [File Upload](#file-upload)
   - [TTS Processing](#tts-processing)
   - [Audio Streaming](#audio-streaming)
   - [Playback Progress](#playback-progress)
   - [Subscription & Payments](#subscription--payments)
   - [User Dashboard & Listening Stats](#user-dashboard--listening-stats) ✨ NEW
   - [Book Covers](#book-covers)
5. [Frontend Implementation Notes](#frontend-implementation-notes)
6. [Error Handling](#error-handling)
7. [Endpoints Summary Table](#endpoints-summary-table)

---

## Overview

### Base URL
```
Production: https://narrafied.com
Local Dev:  http://localhost:8080
```

### Architecture
All requests go through the API Gateway (port 8080), which routes to:
- **Auth Service** (8082): Authentication, user management, subscriptions
- **Content Service** (8083): Books, uploads, TTS, streaming

### Key Features for Frontend
- User login/signup with JWT authentication
- Book CRUD operations
- File upload (PDF, TXT, EPUB, MOBI, AZW, AZW3)
- TTS transcription with progress tracking
- Audio streaming with playback progress
- Stripe subscription management

---

## Authentication

### JWT Token
- **Expiration**: 72 hours
- **Storage**: Store in `localStorage` or secure cookie
- **Header Format**: `Authorization: Bearer {token}`
- **Query Param** (for audio streaming): `?token={token}`

### Example Auth Header
```typescript
const headers = {
  'Content-Type': 'application/json',
  'Authorization': `Bearer ${localStorage.getItem('token')}`
};
```

---

## TypeScript Types

```typescript
// ============================================
// USER TYPES
// ============================================

interface User {
  id: number;
  username: string;
  email: string;
  account_type: 'free' | 'paid';
  is_public: boolean;
  state: string;
  books_read: number;
  created_at: string; // ISO 8601
}

interface UserProfile {
  username: string;
  email: string;
  account_type: 'free' | 'paid';
  is_public: boolean;
  state: string;
  books_read: number;
  created_at: string;
}

interface SignupRequest {
  username: string;
  email: string;
  password: string;
  state: string;
  phone_number?: string;
  device_model?: string;
  device_id?: string;
}

interface SignupResponse {
  message: string;
  user_id: number;
}

interface LoginRequest {
  username: string;
  password: string;
  device_model?: string;
  device_id?: string;
}

interface LoginResponse {
  token: string;
}

// ============================================
// BOOK TYPES
// ============================================

interface Book {
  id: number;
  title: string;
  author: string;
  category: 'Fiction' | 'Non-Fiction';
  genre: string;
  content?: string;
  content_hash?: string;
  file_path: string;
  audio_path: string;
  status: 'pending' | 'processing' | 'completed';
  cover_url: string;
  cover_path: string;
  stream_url: string;
  user_id: number;
  created_at?: string;
  updated_at?: string;
}

interface CreateBookRequest {
  title: string;
  author?: string;
  category: 'Fiction' | 'Non-Fiction';
  genre?: string;
}

interface CreateBookResponse {
  message: string;
  book: Book;
}

interface BooksListResponse {
  books: Book[];
}

// ============================================
// CHUNK/PAGE TYPES
// ============================================

interface BookPage {
  page: number;        // 1-based page number
  content: string;     // Text content (~1000 chars)
  status: 'pending' | 'processing' | 'completed' | 'failed';
  audio_url: string;   // Streaming endpoint for this page
}

interface BookPagesResponse {
  book_id: number;
  title: string;
  status: string;
  total_pages: number;
  limit: number;
  offset: number;
  fully_processed: boolean;
  pages: BookPage[];
}

// ============================================
// FILE UPLOAD TYPES
// ============================================

interface UploadResponse {
  message: string;
  book_id: number;
  total_pages: number;
  file_path: string;
  content_hash: string;
  async: boolean;
  // For async uploads (large files)
  estimated_pages?: number;
  status?: string;
  file_size_mb?: number;
  note?: string;
}

// ============================================
// TTS TYPES
// ============================================

interface TTSRequest {
  book_id: number;
  pages: number[];  // 1-based page numbers, max 2
}

interface TTSResponse {
  message: string;
  audio_paths: string[];
}

interface BatchTTSResponse {
  message: string;
}

// ============================================
// PLAYBACK PROGRESS TYPES
// ============================================

interface PlaybackProgress {
  book_id: number;
  current_position: number;  // seconds
  duration: number;          // seconds
  chunk_index: number;       // 0-based
  completion_percent: number; // 0-100
  play_count: number;
  total_listen_time: number; // seconds
  last_played_at: string;    // RFC3339
}

interface UpdateProgressRequest {
  current_position: number;
  duration?: number;
  chunk_index?: number;
  is_new_session?: boolean;
}

interface UpdateProgressResponse {
  message: string;
  book_id: number;
  current_position: number;
  duration: number;
  chunk_index: number;
  completion_percent: number;
  play_count: number;
  total_listen_time: number;
  last_played_at: string;
}

// ============================================
// SUBSCRIPTION TYPES
// ============================================

interface SubscriptionStatus {
  account_type: 'free' | 'paid';
  has_subscription: boolean;
  subscription_status: string;
  subscription_id?: string;
  current_period_start?: string;
  current_period_end?: string;
  cancel_at_period_end?: boolean;
  plan_name?: string;
  plan_amount?: number;  // cents
  plan_currency?: string;
  plan_interval?: 'month' | 'year';
}

interface CheckoutSessionResponse {
  url: string;  // Stripe checkout URL
}

// ============================================
// ERROR TYPES
// ============================================

interface APIError {
  error: string;
  details?: string;
}

// ============================================
// LISTENING STATS TYPES
// ============================================

interface MostPlayedBook {
  book_id: number;
  title: string;
  author: string;
  genre: string;
  category: string;
  cover_url: string;
  play_count: number;
  total_listen_time: number;  // seconds
  last_played_at: string;     // RFC3339
}

interface MostPlayedResponse {
  most_played: MostPlayedBook[];
  count: number;
  total_plays: number;
  total_listen_time: number;  // seconds
}

interface GenreStats {
  genre: string;
  book_count: number;
  total_plays: number;
  total_listen_time: number;  // seconds
}

interface GenreStatsResponse {
  genres: GenreStats[];
  genre_count: number;
  total_books: number;
  total_plays: number;
  total_listen_time: number;  // seconds
}

// ============================================
// DASHBOARD TYPES (PROPOSED)
// ============================================

interface DashboardUser {
  username: string;
  email: string;
  account_type: 'free' | 'paid';
  books_read: number;
  member_since: string;  // RFC3339
}

interface DashboardSubscription {
  status: string;
  plan_name: string;
  plan_amount: number;         // cents
  current_period_end: string;  // RFC3339
  cancel_at_period_end: boolean;
}

interface DashboardListeningStats {
  total_listen_time: number;           // seconds
  total_listen_time_formatted: string; // e.g., "10h 30m"
  total_plays: number;
  books_in_progress: number;
  books_completed: number;
  favorite_genre: string;
  current_streak_days: number;
}

interface DashboardRecentActivity {
  book_id: number;
  title: string;
  last_position: number;      // seconds
  completion_percent: number;
  last_played_at: string;     // RFC3339
}

interface DashboardResponse {
  user: DashboardUser;
  subscription: DashboardSubscription;
  listening_stats: DashboardListeningStats;
  recent_activity: DashboardRecentActivity[];
}

// ============================================
// PAYMENT HISTORY TYPES (PROPOSED)
// ============================================

interface Payment {
  id: string;                  // Stripe invoice ID
  date: string;                // RFC3339
  amount: number;              // cents
  currency: string;            // e.g., "usd"
  status: 'paid' | 'open' | 'void' | 'uncollectible';
  description: string;
  invoice_pdf: string;         // URL to PDF invoice
  receipt_url: string;         // URL to receipt
}

interface PaymentHistoryResponse {
  payments: Payment[];
  total_spent: number;              // cents
  total_spent_formatted: string;    // e.g., "$19.98"
  payment_count: number;
  has_more: boolean;
}

// ============================================
// BOOK COVER TYPES
// ============================================

interface BookCoverResult {
  url: string;
  source: string;
  description?: string;
}

interface SearchCoversResponse {
  title: string;
  author: string;
  covers: BookCoverResult[];
  message: string;
}

interface SelectCoverRequest {
  cover_url: string;
}

interface SelectCoverResponse {
  message: string;
  cover_url: string;
  local_path: string;
  book_id: number;
}
```

---

## API Endpoints

### Auth & User Management

#### POST /signup
Create a new user account.

```typescript
// Request
const response = await fetch('/signup', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    username: 'john_doe',
    email: 'john@example.com',
    password: 'securepass123',
    state: 'California'
  })
});

// Response: { message: "User registered", user_id: 1 }
```

**Errors:**
- `400`: Invalid input (missing fields, invalid email, password < 6 chars)
- `409`: Email or username already exists

---

#### POST /login
Authenticate and receive JWT token.

```typescript
// Request
const response = await fetch('/login', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    username: 'john_doe',
    password: 'securepass123'
  })
});

// Response: { token: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..." }
const { token } = await response.json();
localStorage.setItem('token', token);
```

**Errors:**
- `401`: Invalid username or password

---

### Social Login

#### POST /auth/apple
Sign in with Apple. The iOS app handles Apple authentication and sends the identity token.

```typescript
// Request
const response = await fetch('/auth/apple', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    identity_token: 'eyJraWQ...',        // JWT from Apple
    user_identifier: '001234.abcd...',   // Apple user ID
    email: 'user@privaterelay.appleid.com', // May be null after first login
    full_name: {
      given_name: 'John',
      family_name: 'Doe'
    } // Only provided on FIRST sign-in
  })
});

// Response (Success)
{
  token: "your_jwt_token_here",
  user: {
    id: 123,
    username: "john_doe",
    email: "user@privaterelay.appleid.com",
    account_type: "free",
    is_new_user: true,
    profile_picture: ""
  }
}
```

**Notes:**
- Email and name are only provided by Apple on the **first sign-in**
- Users can hide their real email (you'll get a private relay address)
- Store email immediately on first sign-in as Apple won't send it again

**Errors:**
- `400`: Invalid request (missing identity_token or user_identifier)
- `401`: Token verification failed or expired

---

#### POST /auth/google
Sign in with Google. The iOS app handles Google authentication and sends the ID token.

```typescript
// Request
const response = await fetch('/auth/google', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    id_token: 'eyJhbG...',    // JWT from Google
    access_token: 'ya29...'   // Optional, for additional API calls
  })
});

// Response (Success)
{
  token: "your_jwt_token_here",
  user: {
    id: 123,
    username: "john_doe",
    email: "john@gmail.com",
    account_type: "free",
    is_new_user: false,
    profile_picture: "https://lh3.googleusercontent.com/..."
  }
}
```

**Errors:**
- `400`: Invalid request (missing id_token)
- `401`: Token verification failed

---

#### POST /auth/facebook
Login with Facebook. The iOS app handles Facebook authentication and sends the access token.

```typescript
// Request
const response = await fetch('/auth/facebook', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    access_token: 'EAABsbCS...',  // Access token from Facebook
    user_id: '1234567890'         // Facebook user ID
  })
});

// Response (Success)
{
  token: "your_jwt_token_here",
  user: {
    id: 123,
    username: "john_doe",
    email: "john@facebook.com",
    account_type: "free",
    is_new_user: true,
    profile_picture: "https://graph.facebook.com/..."
  }
}
```

**Errors:**
- `400`: Invalid request (missing access_token or user_id)
- `401`: Token verification failed or user ID mismatch

---

### Social Login Response Type

```typescript
interface SocialLoginResponse {
  token: string;
  user: {
    id: number;
    username: string;
    email: string;
    account_type: 'free' | 'paid';
    is_new_user: boolean;
    profile_picture?: string;
  };
}

interface SocialLoginError {
  error: 'invalid_request' | 'invalid_token' | 'token_expired' | 'server_error';
  message: string;
}
```

---

#### GET /user/profile
Get current user's profile.

```typescript
// Request (Protected)
const response = await fetch('/user/profile', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response
{
  username: "john_doe",
  email: "john@example.com",
  account_type: "free",
  is_public: true,
  state: "California",
  books_read: 5,
  created_at: "2024-01-15T10:30:00Z"
}
```

---

#### GET /user/account-type
Check subscription tier.

```typescript
// Request (Protected)
const response = await fetch('/user/account-type', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response: { account_type: "free" }
```

---

#### POST /user/deactivate
Temporarily deactivate account.

```typescript
// Request (Protected)
const response = await fetch('/user/deactivate', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  },
  body: JSON.stringify({
    password: 'currentPassword123',
    reason: 'Taking a break'
  })
});
```

---

#### POST /user/delete
Permanently delete account (90-day restoration window).

```typescript
// Request (Protected)
const response = await fetch('/user/delete', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  },
  body: JSON.stringify({
    password: 'currentPassword123',
    reason: 'No longer need the service'
  })
});
```

---

### Book Management

#### POST /user/books
Create a new book record.

```typescript
// Request (Protected)
const response = await fetch('/user/books', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  },
  body: JSON.stringify({
    title: 'The Great Adventure',
    author: 'Jane Smith',
    category: 'Fiction',
    genre: 'Adventure'
  })
});

// Response
{
  message: "Book saved, cover fetching in progress",
  book: {
    id: 123,
    title: "The Great Adventure",
    author: "Jane Smith",
    category: "Fiction",
    genre: "Adventure",
    status: "pending",
    user_id: 1,
    created_at: "2024-01-15T10:30:00Z"
  }
}
```

**Note:** Book covers are automatically fetched via AI web search in the background.

---

#### GET /user/books
List all user's books.

```typescript
// Request (Protected) - with optional filters
const response = await fetch('/user/books?category=Fiction&genre=Mystery', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response
{
  books: [
    {
      id: 123,
      title: "The Great Adventure",
      author: "Jane Smith",
      category: "Fiction",
      genre: "Adventure",
      status: "completed",
      cover_url: "https://narrafied.com/covers/book_123.jpg",
      stream_url: "https://narrafied.com/user/books/stream/proxy/123"
    }
  ]
}
```

---

#### GET /user/books/:book_id
Get a single book.

```typescript
// Request (Protected)
const response = await fetch('/user/books/123', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response includes full book details with content
```

---

#### DELETE /user/books/:book_id
Delete a book.

```typescript
// Request (Protected)
const response = await fetch('/user/books/123', {
  method: 'DELETE',
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response: { message: "Book deleted successfully" }
```

---

#### GET /user/books/:book_id/chunks/pages
List book pages with pagination.

```typescript
// Request (Protected)
const response = await fetch('/user/books/123/chunks/pages?limit=20&offset=0', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response
{
  book_id: 123,
  title: "The Great Adventure",
  status: "processing",
  total_pages: 50,
  limit: 20,
  offset: 0,
  fully_processed: false,
  pages: [
    {
      page: 1,
      content: "Chapter 1: The Beginning...",
      status: "completed",
      audio_url: "/user/books/123/pages/1/audio"
    },
    {
      page: 2,
      content: "It was a dark and stormy night...",
      status: "processing",
      audio_url: "/user/books/123/pages/2/audio"
    }
  ]
}
```

---

### File Upload

#### POST /user/books/upload
Upload a book file (PDF, TXT, EPUB, MOBI, AZW, AZW3).

```typescript
// Request (Protected) - FormData
const formData = new FormData();
formData.append('book_id', '123');
formData.append('file', fileInput.files[0]);

const response = await fetch('/user/books/upload', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${token}` },
  body: formData
});

// Response (small file < 5MB)
{
  message: "File uploaded and split into pages successfully",
  book_id: 123,
  total_pages: 50,
  file_path: "/uploads/book_123.pdf",
  content_hash: "sha256...",
  async: false
}

// Response (large file > 5MB)
{
  message: "File uploaded, chunking in progress (large file)",
  book_id: 123,
  estimated_pages: 200,
  status: "chunking",
  async: true,
  file_size_mb: 15.5,
  note: "Poll GET /user/books/{book_id} to check status..."
}
```

**Supported Formats:**
- `.pdf` - PDF documents
- `.txt` - Plain text files
- `.epub` - EPUB ebooks
- `.mobi` - Kindle MOBI format
- `.azw` - Amazon Kindle format
- `.azw3` - Amazon Kindle KF8 format

**Not Supported:** `.kfx` (convert using Calibre first)

---

### TTS Processing

#### POST /user/books/:book_id/tts/batch
Start batch transcription for entire book.

```typescript
// Request (Protected)
const response = await fetch('/user/books/123/tts/batch', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response: { message: "Batch transcription started in background" }
```

**Process:**
1. Each page: text → TTS audio (OpenAI)
2. Background music generation (ElevenLabs)
3. Foley sound effects overlay
4. Final mixed audio saved

**Polling for Progress:**
```typescript
// Poll every 5 seconds
const checkProgress = async (bookId: number) => {
  const response = await fetch(`/user/books/${bookId}/chunks/pages`, {
    headers: { 'Authorization': `Bearer ${token}` }
  });
  const data = await response.json();

  if (data.fully_processed) {
    console.log('All pages transcribed!');
  } else {
    const completed = data.pages.filter(p => p.status === 'completed').length;
    console.log(`Progress: ${completed}/${data.total_pages}`);
  }
};
```

---

#### POST /user/chunks/tts
Process specific pages (1-2 max).

```typescript
// Request (Protected)
const response = await fetch('/user/chunks/tts', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  },
  body: JSON.stringify({
    book_id: 123,
    pages: [1, 2]  // Max 2 pages
  })
});

// Response
{
  message: "TTS processing complete",
  audio_paths: ["/audio/book_123_page_1.mp3", "/audio/book_123_page_2.mp3"]
}
```

**Free Account Limit:** Can only process 1 chunk total.

---

### Audio Streaming

#### GET /user/books/:book_id/pages/:page/audio
Stream audio for a single page.

```typescript
// In HTML audio element
<audio controls>
  <source src="/user/books/123/pages/1/audio?token=${token}" type="audio/mpeg">
</audio>

// Or programmatically
const audio = new Audio();
audio.src = `/user/books/123/pages/1/audio?token=${token}`;
audio.play();
```

---

#### GET /user/books/stream/proxy/:book_id
Stream entire book audio.

```typescript
// For full book playback
const audioUrl = `/user/books/stream/proxy/123?token=${token}`;

// Works with iOS AVPlayer via query param
```

---

#### GET /user/books/:book_id/chunks/:start/:end/audio
Stream a range of chunks.

```typescript
// Stream chunks 0-10
const audioUrl = `/user/books/123/chunks/0/10/audio?token=${token}`;
```

---

### Playback Progress

#### POST /user/books/:book_id/progress
Save playback position.

```typescript
// Request (Protected)
const response = await fetch('/user/books/123/progress', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  },
  body: JSON.stringify({
    current_position: 125.5,  // seconds
    duration: 3600,           // total duration
    chunk_index: 5,           // current page
    is_new_session: false
  })
});

// Response
{
  message: "Progress updated",
  book_id: 123,
  current_position: 125.5,
  duration: 3600,
  chunk_index: 5,
  completion_percent: 3.49,
  play_count: 2,
  total_listen_time: 500,
  last_played_at: "2024-01-15T15:30:00Z"
}
```

---

#### GET /user/books/:book_id/progress
Get playback position.

```typescript
// Request (Protected)
const response = await fetch('/user/books/123/progress', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response
{
  book_id: 123,
  current_position: 125.5,
  duration: 3600,
  chunk_index: 5,
  completion_percent: 3.49,
  last_played_at: "2024-01-15T15:30:00Z"
}
```

---

#### GET /user/progress
Get all playback progress.

```typescript
// Request (Protected)
const response = await fetch('/user/progress', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response: Array of PlaybackProgress objects
```

---

### Subscription & Payments

#### GET /user/subscription/status
Get current subscription details.

```typescript
// Request (Protected)
const response = await fetch('/user/subscription/status', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response (active subscription)
{
  account_type: "paid",
  has_subscription: true,
  subscription_id: "sub_1234567890",
  subscription_status: "active",
  current_period_start: "2024-01-01T00:00:00Z",
  current_period_end: "2024-02-01T00:00:00Z",
  cancel_at_period_end: false,
  plan_name: "Premium Monthly",
  plan_amount: 999,  // $9.99
  plan_currency: "usd",
  plan_interval: "month"
}
```

---

#### POST /user/stripe/create-checkout-session
Create Stripe checkout for subscription.

```typescript
// Request (Protected)
const response = await fetch('/user/stripe/create-checkout-session', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response: { url: "https://checkout.stripe.com/..." }
const { url } = await response.json();
window.location.href = url;  // Redirect to Stripe
```

---

#### POST /user/subscription/cancel
Cancel subscription (effective at period end).

```typescript
// Request (Protected)
const response = await fetch('/user/subscription/cancel', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response
{
  message: "Subscription canceled successfully",
  subscription_id: "sub_1234567890",
  cancel_at_period_end: true,
  current_period_end: "2024-02-01T00:00:00Z",
  access_until: "2024-02-01T00:00:00Z",
  info: "Your subscription will remain active until Feb 1, 2024"
}
```

---

### User Dashboard & Listening Stats

These endpoints provide valuable user analytics data for building dashboard views.

#### GET /user/stats/most-played
Get user's most played books with listening statistics.

```typescript
// Request (Protected)
const response = await fetch('/user/stats/most-played?limit=10', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response
{
  most_played: [
    {
      book_id: 123,
      title: "The Great Adventure",
      author: "Jane Smith",
      genre: "Adventure",
      category: "Fiction",
      cover_url: "https://narrafied.com/covers/book_123.jpg",
      play_count: 15,
      total_listen_time: 7200,  // seconds (2 hours)
      last_played_at: "2024-01-15T15:30:00Z"
    }
  ],
  count: 5,
  total_plays: 42,
  total_listen_time: 36000  // seconds (10 hours total)
}
```

**Query Parameters:**
- `limit`: integer (default: 10, max: 50)

---

#### GET /user/stats/by-genre
Get listening statistics grouped by genre.

```typescript
// Request (Protected)
const response = await fetch('/user/stats/by-genre', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// Response
{
  genres: [
    {
      genre: "Adventure",
      book_count: 5,
      total_plays: 25,
      total_listen_time: 18000  // seconds
    },
    {
      genre: "Mystery",
      book_count: 3,
      total_plays: 12,
      total_listen_time: 10800
    }
  ],
  genre_count: 4,
  total_books: 12,
  total_plays: 42,
  total_listen_time: 36000
}
```

---

#### GET /user/dashboard ⚠️ PROPOSED - NOT YET IMPLEMENTED
Comprehensive dashboard endpoint combining user stats, subscription, and activity.

> **Note**: This endpoint does not exist yet. It combines data from multiple existing endpoints for frontend convenience. Consider implementing this or call individual endpoints.

```typescript
// PROPOSED Request (Protected)
const response = await fetch('/user/dashboard', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// PROPOSED Response
{
  user: {
    username: "john_doe",
    email: "john@example.com",
    account_type: "paid",
    books_read: 12,
    member_since: "2024-01-01T00:00:00Z"
  },
  subscription: {
    status: "active",
    plan_name: "Premium Monthly",
    plan_amount: 999,
    current_period_end: "2024-02-01T00:00:00Z",
    cancel_at_period_end: false
  },
  listening_stats: {
    total_listen_time: 36000,        // seconds
    total_listen_time_formatted: "10h 0m",
    total_plays: 42,
    books_in_progress: 3,
    books_completed: 9,
    favorite_genre: "Adventure",
    current_streak_days: 5           // consecutive days listened
  },
  recent_activity: [
    {
      book_id: 123,
      title: "The Great Adventure",
      last_position: 1250,           // seconds
      completion_percent: 45,
      last_played_at: "2024-01-15T15:30:00Z"
    }
  ]
}
```

**Workaround** (until endpoint is implemented):
```typescript
// Call multiple endpoints in parallel
const [profile, subscription, mostPlayed, byGenre, progress] = await Promise.all([
  fetch('/user/profile', { headers }),
  fetch('/user/subscription/status', { headers }),
  fetch('/user/stats/most-played?limit=5', { headers }),
  fetch('/user/stats/by-genre', { headers }),
  fetch('/user/progress', { headers })
]).then(responses => Promise.all(responses.map(r => r.json())));

// Combine into dashboard data
const dashboard = {
  user: profile,
  subscription: subscription,
  listening_stats: {
    total_listen_time: byGenre.total_listen_time,
    total_plays: byGenre.total_plays,
    total_books: byGenre.total_books
  },
  most_played: mostPlayed.most_played,
  recent_activity: progress
};
```

---

#### GET /user/payment-history ⚠️ PROPOSED - NOT YET IMPLEMENTED
Get user's payment and invoice history from Stripe.

> **Note**: This endpoint needs to be implemented in the auth-service using Stripe's Invoice API.

```typescript
// PROPOSED Request (Protected)
const response = await fetch('/user/payment-history?limit=10', {
  headers: { 'Authorization': `Bearer ${token}` }
});

// PROPOSED Response
{
  payments: [
    {
      id: "in_1234567890",
      date: "2024-01-01T00:00:00Z",
      amount: 999,                    // cents ($9.99)
      currency: "usd",
      status: "paid",                 // "paid", "open", "void", "uncollectible"
      description: "Premium Monthly Subscription",
      invoice_pdf: "https://pay.stripe.com/invoice/...",
      receipt_url: "https://pay.stripe.com/receipts/..."
    },
    {
      id: "in_0987654321",
      date: "2023-12-01T00:00:00Z",
      amount: 999,
      currency: "usd",
      status: "paid",
      description: "Premium Monthly Subscription",
      invoice_pdf: "https://pay.stripe.com/invoice/...",
      receipt_url: "https://pay.stripe.com/receipts/..."
    }
  ],
  total_spent: 1998,                  // cents ($19.98)
  total_spent_formatted: "$19.98",
  payment_count: 2,
  has_more: false
}
```

**Query Parameters:**
- `limit`: integer (default: 10, max: 100)
- `starting_after`: string (Stripe invoice ID for pagination)

**Implementation Notes for Backend:**
```go
// In auth-service/main.go - use Stripe Invoice API
import "github.com/stripe/stripe-go/v78/invoice"

// List invoices for customer
params := &stripe.InvoiceListParams{
    Customer: stripe.String(user.StripeCustomerID),
}
params.Limit = stripe.Int64(10)
iter := invoice.List(params)
```

---

### Book Covers

#### POST /user/search-book-covers
Search for alternative book covers.

```typescript
// Request (Protected)
const response = await fetch('/user/search-book-covers', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  },
  body: JSON.stringify({
    title: 'The Great Adventure',
    author: 'Jane Smith',
    book_id: 123  // Optional
  })
});

// Response
{
  title: "The Great Adventure",
  author: "Jane Smith",
  covers: [
    { url: "https://...", source: "narrafied.com", description: "Auto-fetched cover" },
    { url: "https://...", source: "amazon" },
    { url: "https://...", source: "goodreads" }
  ],
  message: "Found 3 covers"
}
```

---

#### POST /user/books/:book_id/select-cover
Select and save a cover image.

```typescript
// Request (Protected)
const response = await fetch('/user/books/123/select-cover', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  },
  body: JSON.stringify({
    cover_url: 'https://example.com/cover.jpg'
  })
});

// Response
{
  message: "Cover saved successfully",
  cover_url: "https://narrafied.com/covers/book_123.jpg",
  local_path: "/covers/book_123.jpg",
  book_id: 123
}
```

---

#### POST /user/books/:book_id/cover (Legacy)
Upload a cover image manually.

```typescript
// Request (Protected) - FormData
const formData = new FormData();
formData.append('cover', fileInput.files[0]);

const response = await fetch('/user/books/123/cover', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${token}` },
  body: formData
});

// Response: { message: "upload in progress", cover_url: "https://..." }
```

---

## Frontend Implementation Notes

### 1. Authentication Flow
```typescript
// auth.ts
class AuthService {
  private token: string | null = null;

  async login(username: string, password: string): Promise<boolean> {
    const response = await fetch('/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password })
    });

    if (response.ok) {
      const { token } = await response.json();
      this.token = token;
      localStorage.setItem('token', token);
      return true;
    }
    return false;
  }

  getToken(): string | null {
    return this.token || localStorage.getItem('token');
  }

  isLoggedIn(): boolean {
    return !!this.getToken();
  }

  logout(): void {
    this.token = null;
    localStorage.removeItem('token');
  }
}
```

### 2. API Client
```typescript
// api.ts
class APIClient {
  private baseUrl = '';

  private getHeaders(): HeadersInit {
    const token = localStorage.getItem('token');
    return {
      'Content-Type': 'application/json',
      ...(token && { 'Authorization': `Bearer ${token}` })
    };
  }

  async get<T>(path: string): Promise<T> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      headers: this.getHeaders()
    });
    if (!response.ok) throw await response.json();
    return response.json();
  }

  async post<T>(path: string, body?: object): Promise<T> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      method: 'POST',
      headers: this.getHeaders(),
      body: body ? JSON.stringify(body) : undefined
    });
    if (!response.ok) throw await response.json();
    return response.json();
  }

  async delete<T>(path: string): Promise<T> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      method: 'DELETE',
      headers: this.getHeaders()
    });
    if (!response.ok) throw await response.json();
    return response.json();
  }

  async uploadFile(path: string, formData: FormData): Promise<UploadResponse> {
    const token = localStorage.getItem('token');
    const response = await fetch(`${this.baseUrl}${path}`, {
      method: 'POST',
      headers: token ? { 'Authorization': `Bearer ${token}` } : {},
      body: formData
    });
    if (!response.ok) throw await response.json();
    return response.json();
  }
}
```

### 3. Audio Player Component
```typescript
// AudioPlayer.ts
class AudioPlayer {
  private audio: HTMLAudioElement;
  private bookId: number;
  private token: string;

  constructor(bookId: number) {
    this.audio = new Audio();
    this.bookId = bookId;
    this.token = localStorage.getItem('token') || '';

    this.audio.addEventListener('timeupdate', () => this.saveProgress());
  }

  playPage(page: number): void {
    this.audio.src = `/user/books/${this.bookId}/pages/${page}/audio?token=${this.token}`;
    this.audio.play();
  }

  playFullBook(): void {
    this.audio.src = `/user/books/stream/proxy/${this.bookId}?token=${this.token}`;
    this.audio.play();
  }

  private async saveProgress(): Promise<void> {
    // Debounce: save every 10 seconds
    await fetch(`/user/books/${this.bookId}/progress`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${this.token}`
      },
      body: JSON.stringify({
        current_position: this.audio.currentTime,
        duration: this.audio.duration
      })
    });
  }
}
```

### 4. TTS Processing with Progress
```typescript
// ttsProcessor.ts
class TTSProcessor {
  async startBatchProcessing(bookId: number): Promise<void> {
    await api.post(`/user/books/${bookId}/tts/batch`);
  }

  async pollProgress(bookId: number, onProgress: (percent: number) => void): Promise<void> {
    const poll = async () => {
      const data = await api.get<BookPagesResponse>(
        `/user/books/${bookId}/chunks/pages`
      );

      const completed = data.pages.filter(p => p.status === 'completed').length;
      const percent = (completed / data.total_pages) * 100;

      onProgress(percent);

      if (!data.fully_processed) {
        setTimeout(poll, 5000);  // Poll every 5 seconds
      }
    };

    poll();
  }
}
```

---

## Error Handling

### Standard Error Response
```typescript
interface APIError {
  error: string;
  details?: string;
}
```

### HTTP Status Codes
| Code | Meaning |
|------|---------|
| 200 | Success |
| 202 | Accepted (async processing started) |
| 400 | Bad Request (invalid input) |
| 401 | Unauthorized (missing/invalid token) |
| 403 | Forbidden (insufficient permissions) |
| 404 | Not Found |
| 409 | Conflict (duplicate email/username) |
| 500 | Server Error |

### Error Handling Example
```typescript
try {
  const books = await api.get<BooksListResponse>('/user/books');
} catch (error) {
  if (error.error === 'Invalid token') {
    // Redirect to login
    window.location.href = '/login';
  } else {
    // Show error message
    showToast(error.error || 'An error occurred');
  }
}
```

---

## Quick Reference: Core User Flows

### 1. Signup → Login → View Books
```
POST /signup → POST /login → GET /user/books
```

### 2. Create & Upload Book
```
POST /user/books → POST /user/books/upload (FormData)
```

### 3. Transcribe Book
```
POST /user/books/:id/tts/batch → Poll GET /user/books/:id/chunks/pages
```

### 4. Listen to Book
```
GET /user/books/:id/pages/:page/audio?token=...
  or
GET /user/books/stream/proxy/:id?token=...
```

### 5. Subscribe to Premium
```
POST /user/stripe/create-checkout-session → Redirect to Stripe URL
```

### 6. View User Dashboard & Stats
```
GET /user/profile + GET /user/subscription/status + GET /user/stats/most-played + GET /user/stats/by-genre
  (Call in parallel, combine results)
```

### 7. View Payment History ⚠️ (Needs Implementation)
```
GET /user/payment-history
```

---

## Account Limits

| Feature | Free | Paid |
|---------|------|------|
| Books | Unlimited | Unlimited |
| TTS Processing | 1 chunk max | Unlimited |
| Audio Streaming | Yes | Yes |
| Background Music | Yes | Yes |
| Sound Effects | Yes | Yes |

---

## Endpoints Summary Table

### All API Endpoints at a Glance

| Method | Endpoint | Auth | Description | Status |
|--------|----------|------|-------------|--------|
| **Auth & User** |
| POST | `/signup` | No | Create new user account | ✅ |
| POST | `/login` | No | Authenticate, get JWT token | ✅ |
| POST | `/restore-account` | No | Restore deleted account | ✅ |
| GET | `/user/profile` | Yes | Get user profile | ✅ |
| GET | `/user/account-type` | Yes | Get account tier (free/paid) | ✅ |
| POST | `/user/activity/ping` | Yes | Update last activity | ✅ |
| POST | `/user/deactivate` | Yes | Temporarily deactivate account | ✅ |
| POST | `/user/delete` | Yes | Permanently delete account | ✅ |
| **Social Login** |
| POST | `/auth/apple` | No | Sign in with Apple | ✅ NEW |
| POST | `/auth/google` | No | Sign in with Google | ✅ NEW |
| POST | `/auth/facebook` | No | Login with Facebook | ✅ NEW |
| **Books** |
| POST | `/user/books` | Yes | Create new book | ✅ |
| GET | `/user/books` | Yes | List user's books | ✅ |
| GET | `/user/books/:id` | Yes | Get single book | ✅ |
| DELETE | `/user/books/:id` | Yes | Delete book | ✅ |
| GET | `/user/books/:id/chunks/pages` | Yes | List book pages | ✅ |
| **File Upload** |
| POST | `/user/books/upload` | Yes | Upload book file (PDF/TXT/EPUB/MOBI/AZW) | ✅ |
| **TTS Processing** |
| POST | `/user/books/:id/tts/batch` | Yes | Start batch transcription | ✅ |
| POST | `/user/chunks/tts` | Yes | Process specific pages | ✅ |
| **Audio Streaming** |
| GET | `/user/books/:id/pages/:page/audio` | Yes | Stream single page | ✅ |
| GET | `/user/books/stream/proxy/:id` | Yes | Stream full book | ✅ |
| GET | `/user/books/:id/chunks/:start/:end/audio` | Yes | Stream chunk range | ✅ |
| **Playback Progress** |
| POST | `/user/books/:id/progress` | Yes | Save playback position | ✅ |
| GET | `/user/books/:id/progress` | Yes | Get playback position | ✅ |
| GET | `/user/progress` | Yes | Get all progress | ✅ |
| DELETE | `/user/books/:id/progress` | Yes | Reset progress | ✅ |
| **Subscription** |
| GET | `/user/subscription/status` | Yes | Get subscription details | ✅ |
| POST | `/user/stripe/create-checkout-session` | Yes | Create Stripe checkout | ✅ |
| POST | `/user/subscription/cancel` | Yes | Cancel subscription | ✅ |
| **Dashboard & Stats** |
| GET | `/user/stats/most-played` | Yes | Most played books with listen time | ✅ |
| GET | `/user/stats/by-genre` | Yes | Stats grouped by genre | ✅ |
| GET | `/user/dashboard` | Yes | Combined dashboard data | ⚠️ Proposed |
| GET | `/user/payment-history` | Yes | Payment/invoice history | ⚠️ Proposed |
| **Book Covers** |
| POST | `/user/search-book-covers` | Yes | Search for book covers | ✅ |
| POST | `/user/books/:id/select-cover` | Yes | Select and save cover | ✅ |
| POST | `/user/books/:id/cover` | Yes | Upload cover manually | ✅ |

**Legend:**
- ✅ = Implemented and ready to use
- ⚠️ Proposed = Not yet implemented (see documentation for workarounds)

---

## Implementation Priority for Proposed Endpoints

### 1. Payment History (High Priority)
Stripe stores all invoice data. Implementation requires:
- Import `github.com/stripe/stripe-go/v78/invoice`
- List invoices for customer
- Return formatted payment history

### 2. Dashboard Endpoint (Medium Priority)
Convenience endpoint that aggregates existing data. Until implemented, frontend can call multiple endpoints in parallel.

---

*Document generated for frontend development reference. Last updated: December 2024*
