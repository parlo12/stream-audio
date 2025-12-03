# iOS App Integration Guide - Automatic Book Cover Fetching

## Overview

This document provides instructions for updating the iOS app to integrate with the new automatic book cover fetching feature in the Stream Audio backend. The backend now automatically fetches book covers from the web when users create books, eliminating the need for manual cover uploads.

---

## Backend Changes Summary

### What Changed in the Microservice

1. **Automatic Cover Fetching**: When a user creates a book via `POST /user/books`, the backend now automatically:
   - Searches the web for the book cover using OpenAI's Responses API with web search
   - Downloads the cover image (target: 1000×1600px, aspect ratio 0.625)
   - Saves it to `/uploads/covers/` directory
   - Updates the book record with `cover_url` and `cover_path`
   - Publishes an MQTT event to `users/{user_id}/cover_uploaded` with `source: "web_search"`

2. **Response Message Updated**: The book creation endpoint now returns:
   ```json
   {
     "message": "Book saved, cover fetching in progress",
     "book": { /* book object */ }
   }
   ```

3. **MQTT Event Enhanced**: Cover upload events now include a `source` field:
   ```json
   {
     "book_id": 123,
     "cover_url": "https://server.com/covers/123_1733097600.jpg",
     "timestamp": "2025-12-01T12:00:00Z",
     "source": "web_search"  // NEW: "web_search" or "manual_upload"
   }
   ```

4. **Legacy Endpoint Retained**: `POST /user/books/:book_id/cover` still works for manual uploads as a fallback

---

## iOS Changes Required

### File to Modify: `UploadBookView.swift`

The following changes need to be made to `UploadBookView.swift` to support automatic cover fetching:

---

## Step-by-Step iOS Implementation

### 1. Remove Manual Cover Upload Requirement

**Before**: Users had to manually upload a book cover after creating a book.

**After**: Cover upload should be optional (only needed if automatic fetch fails).

#### Changes to Make:

**A. Update Book Creation Flow**

Locate the function that creates a book (likely `createBook()` or similar) and modify it:

```swift
// OLD CODE (Remove or comment out):
func createBook() {
    // ... create book API call ...
    // After success, navigate to cover upload screen
    navigateToUploadCover(bookId: createdBook.id)
}

// NEW CODE:
func createBook() {
    guard let title = bookTitle, !title.isEmpty,
          let author = bookAuthor, !author.isEmpty,
          let category = bookCategory, !category.isEmpty else {
        showError("Please fill in all required fields")
        return
    }

    isLoading = true
    errorMessage = nil

    let bookData: [String: Any] = [
        "title": title,
        "author": author,
        "category": category,
        "genre": bookGenre ?? ""
    ]

    APIClient.shared.post("/user/books", body: bookData) { [weak self] result in
        DispatchQueue.main.async {
            self?.isLoading = false

            switch result {
            case .success(let response):
                if let book = response["book"] as? [String: Any],
                   let bookId = book["ID"] as? Int {

                    // NEW: Show cover fetching in progress message
                    self?.showCoverFetchingMessage()

                    // NEW: Start monitoring for cover updates
                    self?.monitorCoverFetch(bookId: bookId)

                    // Save the created book
                    self?.createdBook = book

                    // Navigate to next step (upload document, etc.)
                    self?.navigateToDocumentUpload(bookId: bookId)
                }

            case .failure(let error):
                self?.showError("Failed to create book: \(error.localizedDescription)")
            }
        }
    }
}
```

---

### 2. Add Cover Fetching Status UI

Add UI elements to show the user that the cover is being automatically fetched:

```swift
// Add to your view's state variables
@State private var isCoverFetching = false
@State private var coverFetchStatus: String = ""
@State private var fetchedCoverURL: String?

// Add a status view in your SwiftUI body
var body: some View {
    VStack {
        // ... existing form fields ...

        // NEW: Cover fetching status indicator
        if isCoverFetching {
            HStack {
                ProgressView()
                    .progressViewStyle(CircularProgressViewStyle())

                VStack(alignment: .leading) {
                    Text("Fetching book cover...")
                        .font(.subheadline)
                        .foregroundColor(.orange)

                    if !coverFetchStatus.isEmpty {
                        Text(coverFetchStatus)
                            .font(.caption)
                            .foregroundColor(.gray)
                    }
                }
            }
            .padding()
            .background(Color.orange.opacity(0.1))
            .cornerRadius(8)
        }

        // NEW: Show fetched cover preview (optional)
        if let coverURL = fetchedCoverURL {
            AsyncImage(url: URL(string: coverURL)) { image in
                image
                    .resizable()
                    .aspectRatio(0.625, contentMode: .fit)
                    .frame(width: 120, height: 192)
                    .cornerRadius(8)
            } placeholder: {
                ProgressView()
            }

            Text("✓ Cover fetched successfully!")
                .font(.caption)
                .foregroundColor(.green)
        }

        // ... rest of your view ...
    }
}
```

---

### 3. Implement Cover Fetch Monitoring

Add methods to monitor when the cover is ready:

```swift
// OPTION A: Using MQTT (Recommended for real-time updates)
func monitorCoverFetch(bookId: Int) {
    isCoverFetching = true
    coverFetchStatus = "Searching for cover online..."

    // Subscribe to MQTT topic for this user
    if let userId = AuthManager.shared.currentUserId {
        MQTTManager.shared.subscribeToCoverUpdates(userId: userId)

        // Set up listener for this specific book
        MQTTManager.shared.onCoverUploaded = { [weak self] event in
            if event.book_id == bookId && event.source == "web_search" {
                DispatchQueue.main.async {
                    self?.isCoverFetching = false
                    self?.fetchedCoverURL = event.cover_url
                    self?.coverFetchStatus = "Cover ready!"

                    // Optional: Update the book object
                    self?.updateBookCover(url: event.cover_url)
                }
            }
        }
    }

    // Set a timeout fallback (30 seconds)
    DispatchQueue.main.asyncAfter(deadline: .now() + 30) { [weak self] in
        if self?.isCoverFetching == true {
            self?.handleCoverFetchTimeout(bookId: bookId)
        }
    }
}

// OPTION B: Using Polling (Fallback if MQTT is not available)
func monitorCoverFetchWithPolling(bookId: Int) {
    isCoverFetching = true
    coverFetchStatus = "Searching for cover online..."

    var attempts = 0
    let maxAttempts = 15 // 30 seconds total (2 second intervals)

    Timer.scheduledTimer(withTimeInterval: 2.0, repeats: true) { [weak self] timer in
        attempts += 1

        // Fetch book details
        APIClient.shared.get("/user/books/\(bookId)") { result in
            DispatchQueue.main.async {
                switch result {
                case .success(let response):
                    if let book = response["book"] as? [String: Any],
                       let coverURL = book["cover_url"] as? String,
                       !coverURL.isEmpty {

                        // Cover is ready!
                        self?.isCoverFetching = false
                        self?.fetchedCoverURL = coverURL
                        self?.coverFetchStatus = "Cover ready!"
                        timer.invalidate()

                    } else if attempts >= maxAttempts {
                        // Timeout
                        timer.invalidate()
                        self?.handleCoverFetchTimeout(bookId: bookId)
                    } else {
                        // Still waiting
                        self?.coverFetchStatus = "Still searching... (\(attempts * 2)s)"
                    }

                case .failure:
                    // Continue polling on error
                    if attempts >= maxAttempts {
                        timer.invalidate()
                        self?.handleCoverFetchTimeout(bookId: bookId)
                    }
                }
            }
        }
    }
}

// Handle timeout - offer manual upload
func handleCoverFetchTimeout(bookId: Int) {
    isCoverFetching = false

    let alert = UIAlertController(
        title: "Cover Not Found",
        message: "We couldn't find a cover for this book automatically. Would you like to upload one manually?",
        preferredStyle: .alert
    )

    alert.addAction(UIAlertAction(title: "Upload Cover", style: .default) { [weak self] _ in
        self?.navigateToManualCoverUpload(bookId: bookId)
    })

    alert.addAction(UIAlertAction(title: "Skip for Now", style: .cancel) { [weak self] _ in
        self?.coverFetchStatus = "No cover available"
    })

    // Present alert (adjust based on your view hierarchy)
    if let viewController = UIApplication.shared.windows.first?.rootViewController {
        viewController.present(alert, animated: true)
    }
}
```

---

### 4. Update MQTT Manager (If Using MQTT)

Add or update your MQTT manager to handle cover upload events:

```swift
// MQTTManager.swift

import CocoaMQTT

class MQTTManager: NSObject, ObservableObject {
    static let shared = MQTTManager()

    private var mqttClient: CocoaMQTT?
    var onCoverUploaded: ((CoverUploadedEvent) -> Void)?

    func subscribeToCoverUpdates(userId: Int) {
        let topic = "users/\(userId)/cover_uploaded"
        mqttClient?.subscribe(topic, qos: .qos1)
    }

    // MQTT Delegate Method
    func mqtt(_ mqtt: CocoaMQTT, didReceiveMessage message: CocoaMQTTMessage, id: UInt16) {
        guard let payloadString = message.string,
              message.topic.contains("cover_uploaded") else { return }

        do {
            let decoder = JSONDecoder()
            let event = try decoder.decode(CoverUploadedEvent.self,
                                          from: Data(payloadString.utf8))

            // Call the callback
            DispatchQueue.main.async {
                self.onCoverUploaded?(event)
            }

        } catch {
            print("Error decoding MQTT cover upload event: \(error)")
        }
    }
}

// Model for MQTT event
struct CoverUploadedEvent: Codable {
    let book_id: Int
    let cover_url: String
    let timestamp: String
    let source: String  // "web_search" or "manual_upload"
}
```

---

### 5. Update Book List View (Optional Enhancement)

Update your book list to show cover loading state:

```swift
// In your BookRow or BookCell view
struct BookRow: View {
    let book: Book
    @State private var coverImage: UIImage?
    @State private var isLoadingCover = false

    var body: some View {
        HStack {
            // Cover Image
            Group {
                if let image = coverImage {
                    Image(uiImage: image)
                        .resizable()
                        .aspectRatio(0.625, contentMode: .fit)
                        .frame(width: 60, height: 96)
                        .cornerRadius(4)
                } else if isLoadingCover || book.coverURL.isEmpty {
                    ZStack {
                        Rectangle()
                            .fill(Color.gray.opacity(0.2))
                            .frame(width: 60, height: 96)
                            .cornerRadius(4)

                        if isLoadingCover {
                            ProgressView()
                                .scaleEffect(0.7)
                        } else {
                            Image(systemName: "book.closed")
                                .foregroundColor(.gray)
                        }
                    }
                }
            }

            VStack(alignment: .leading, spacing: 4) {
                Text(book.title)
                    .font(.headline)

                Text(book.author)
                    .font(.subheadline)
                    .foregroundColor(.secondary)

                // NEW: Show fetching status
                if book.coverURL.isEmpty {
                    Text("Fetching cover...")
                        .font(.caption)
                        .foregroundColor(.orange)
                }
            }
        }
        .onAppear {
            loadCover()
        }
    }

    func loadCover() {
        guard !book.coverURL.isEmpty else {
            isLoadingCover = true
            // Could start polling here for newly created books
            return
        }

        isLoadingCover = true

        URLSession.shared.dataTask(with: URL(string: book.coverURL)!) { data, response, error in
            if let data = data, let image = UIImage(data: data) {
                DispatchQueue.main.async {
                    self.coverImage = image
                    self.isLoadingCover = false
                }
            } else {
                DispatchQueue.main.async {
                    self.isLoadingCover = false
                }
            }
        }.resume()
    }
}
```

---

### 6. Update Manual Upload Flow (Fallback)

Keep the manual upload functionality as a fallback, but make it optional:

```swift
func navigateToManualCoverUpload(bookId: Int) {
    // Show image picker
    let picker = UIImagePickerController()
    picker.delegate = self
    picker.sourceType = .photoLibrary
    picker.allowsEditing = true

    // Store book ID for use in delegate
    self.currentUploadBookId = bookId

    // Present picker
    if let viewController = UIApplication.shared.windows.first?.rootViewController {
        viewController.present(picker, animated: true)
    }
}

// UIImagePickerControllerDelegate
func imagePickerController(_ picker: UIImagePickerController,
                          didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey : Any]) {
    picker.dismiss(animated: true)

    guard let image = info[.editedImage] as? UIImage ?? info[.originalImage] as? UIImage,
          let bookId = currentUploadBookId,
          let imageData = image.jpegData(compressionQuality: 0.8) else {
        return
    }

    uploadCoverManually(bookId: bookId, imageData: imageData)
}

func uploadCoverManually(bookId: Int, imageData: Data) {
    isLoading = true

    APIClient.shared.uploadCover(bookId: bookId, imageData: imageData) { [weak self] result in
        DispatchQueue.main.async {
            self?.isLoading = false

            switch result {
            case .success(let response):
                if let coverURL = response["cover_url"] as? String {
                    self?.fetchedCoverURL = coverURL
                    self?.showSuccess("Cover uploaded successfully!")
                }

            case .failure(let error):
                self?.showError("Failed to upload cover: \(error.localizedDescription)")
            }
        }
    }
}
```

---

## Summary of Changes

### ✅ What to Add to `UploadBookView.swift`

1. **State Variables**:
   ```swift
   @State private var isCoverFetching = false
   @State private var coverFetchStatus: String = ""
   @State private var fetchedCoverURL: String?
   @State private var currentUploadBookId: Int?
   ```

2. **UI Components**:
   - Cover fetching progress indicator
   - Status message display
   - Cover preview (when fetched)
   - Manual upload button (optional/fallback)

3. **Functions**:
   - `monitorCoverFetch(bookId:)` - Monitor via MQTT or polling
   - `handleCoverFetchTimeout(bookId:)` - Offer manual upload
   - `navigateToManualCoverUpload(bookId:)` - Fallback upload
   - `uploadCoverManually(bookId:imageData:)` - Manual upload API call

4. **Behavior Changes**:
   - Book creation no longer requires immediate cover upload
   - Cover upload becomes optional (only if automatic fetch fails)
   - Show real-time feedback about cover fetching progress
   - Automatically update UI when cover is ready

---

## Testing Checklist

After implementing these changes, test the following scenarios:

- [ ] Create a book with a well-known title (e.g., "Harry Potter") - cover should auto-fetch
- [ ] Create a book with an obscure title - should timeout and offer manual upload
- [ ] Verify MQTT events are received (if using MQTT)
- [ ] Verify polling works (if using polling fallback)
- [ ] Test manual upload fallback when auto-fetch fails
- [ ] Verify book list shows loading state for new books
- [ ] Verify cover images display correctly once fetched
- [ ] Test network failures (airplane mode) - should handle gracefully
- [ ] Verify covers persist across app restarts

---

## API Endpoints Reference

### Book Creation
```
POST /user/books
Headers:
  Authorization: Bearer {token}
  Content-Type: application/json

Body:
{
  "title": "Book Title",
  "author": "Author Name",
  "category": "Fiction",
  "genre": "Fantasy"
}

Response:
{
  "message": "Book saved, cover fetching in progress",
  "book": {
    "ID": 123,
    "Title": "Book Title",
    "Author": "Author Name",
    "CoverURL": "",  // Will be populated later
    "CoverPath": "",
    ...
  }
}
```

### Get Book Details (For Polling)
```
GET /user/books/{book_id}
Headers:
  Authorization: Bearer {token}

Response:
{
  "book": {
    "ID": 123,
    "cover_url": "https://server.com/covers/123_1733097600.jpg",  // Populated when ready
    ...
  }
}
```

### Manual Cover Upload (Fallback)
```
POST /user/books/{book_id}/cover
Headers:
  Authorization: Bearer {token}
  Content-Type: multipart/form-data

Body:
  cover: (binary image data)

Response:
{
  "message": "upload in progress",
  "cover_url": "https://server.com/covers/123_1733097600.jpg"
}
```

---

## MQTT Topic Reference

### Subscribe To:
```
users/{user_id}/cover_uploaded
```

### Event Payload:
```json
{
  "book_id": 123,
  "cover_url": "https://server.com/covers/123_1733097600.jpg",
  "timestamp": "2025-12-01T12:00:00Z",
  "source": "web_search"
}
```

**Note**: The `source` field will be:
- `"web_search"` - Automatically fetched by backend
- `"manual_upload"` - Manually uploaded by user

---

## Performance Considerations

**Cover Fetch Timing**:
- Typical: 5-15 seconds
- Maximum: 60 seconds (with timeout)

**iOS App Recommendations**:
1. **Non-blocking**: Don't block user from continuing with book setup
2. **Show progress**: Display loading indicator and status messages
3. **Cache covers**: Use URLCache or custom caching for fetched covers
4. **Handle failures**: Always offer manual upload as fallback
5. **Use MQTT**: More efficient than polling for real-time updates

---

## Migration Path

For existing users who already have books without covers:

```swift
// Optional: Batch fetch covers for existing books
func fetchMissingCovers() {
    // Get all books without covers
    let booksWithoutCovers = books.filter { $0.coverURL.isEmpty }

    for book in booksWithoutCovers {
        // Backend can trigger cover fetch for existing books
        // This would require a new endpoint like:
        // POST /user/books/{book_id}/fetch-cover

        APIClient.shared.post("/user/books/\(book.id)/fetch-cover") { result in
            // Handle response
        }
    }
}
```

---

## Questions or Issues?

If you encounter any issues during implementation:

1. Check backend logs: `docker compose logs -f content-service`
2. Verify MQTT broker is running and accessible
3. Test API endpoints using Postman/curl first
4. Check that OPENAI_API_KEY is set in backend environment

Refer to the full API documentation in `API_CHANGELOG.md` for more details.
