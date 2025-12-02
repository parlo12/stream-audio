# Stream Audio API Changelog & iOS Integration Guide

> **Purpose**: This document tracks backend API changes and feature updates for the Stream Audio iOS app integration.
> **Last Updated**: December 1, 2025
> **Backend Version**: 2.1.0

---

## 📅 Latest Update: December 1, 2025

### Feature: Automatic Book Cover Fetching via OpenAI Web Search

#### Summary
The system now **automatically fetches book covers** from the web when a user creates a new book. This eliminates the need for manual cover uploads. The backend uses OpenAI's Responses API with web search to find official book covers matching the book's title and author.

#### What Changed

**Before**:
- Users had to manually upload book covers using `POST /user/books/:book_id/cover`
- Book creation returned without a cover
- Cover upload was a separate, manual step

**After**:
- Book covers are **automatically fetched** during book creation
- No user action required
- Cover fetching happens asynchronously in the background
- Uses OpenAI web search to find official covers from reputable sources
- Target dimensions: **1000px × 1600px** (aspect ratio 0.625)
- Sources: Amazon, Goodreads, publisher websites, book retailers

#### API Changes

##### Modified Endpoint: `POST /user/books`

**Request** (No changes):
```json
{
  "title": "The Great Gatsby",
  "author": "F. Scott Fitzgerald",
  "category": "Fiction",
  "genre": "Classic"
}
```

**Response** (Updated message):
```json
{
  "message": "Book saved, cover fetching in progress",
  "book": {
    "id": 123,
    "title": "The Great Gatsby",
    "author": "F. Scott Fitzgerald",
    "category": "Fiction",
    "genre": "Classic",
    "status": "pending",
    "user_id": 456,
    "cover_url": "",  // Will be populated asynchronously
    "cover_path": ""  // Will be populated asynchronously
  }
}
```

**Background Process**:
After the book is created, the backend automatically:
1. Searches the web for the book cover using OpenAI's Responses API
2. Downloads the cover image
3. Saves it to `/uploads/covers/`
4. Updates the book record with `cover_url` and `cover_path`
5. Publishes an MQTT event to `users/{user_id}/cover_uploaded`

**MQTT Event** (New field):
```json
{
  "book_id": 123,
  "cover_url": "https://your-server.com/covers/123_1733097600.jpg",
  "timestamp": "2025-12-01T12:00:00Z",
  "source": "web_search"  // NEW: Indicates automatic fetching
}
```

#### Legacy Endpoint (Still Available)

**Endpoint**: `POST /user/books/:book_id/cover`

This endpoint is still available for **manual cover uploads** if:
- The automatic search fails to find a cover
- The user wants to use a custom cover image
- The automatically fetched cover is incorrect

---

## 📱 iOS Implementation Guide for Automatic Cover Fetching

### Required Changes

#### 1. Update Book Creation Response Handling

**Before**:
```swift
func createBook(title: String, author: String, category: String, genre: String) {
    let bookData = [
        "title": title,
        "author": author,
        "category": category,
        "genre": genre
    ]

    APIClient.shared.post("/user/books", body: bookData) { result in
        switch result {
        case .success(let response):
            // Book created, no cover yet
            print("Book created: \(response.book.id)")
        case .failure(let error):
            print("Error: \(error)")
        }
    }
}
```

**After**:
```swift
func createBook(title: String, author: String, category: String, genre: String) {
    let bookData = [
        "title": title,
        "author": author,
        "category": category,
        "genre": genre
    ]

    APIClient.shared.post("/user/books", body: bookData) { result in
        switch result {
        case .success(let response):
            // Book created, cover fetching automatically in background
            print("Book created: \(response.book.id)")
            print("Cover fetching in progress...")

            // Subscribe to MQTT topic for cover updates
            self.subscribeToCoverUpdates(userId: self.currentUserId)

            // Optionally: Poll for cover updates
            self.pollForCoverUpdate(bookId: response.book.id)

        case .failure(let error):
            print("Error: \(error)")
        }
    }
}
```

#### 2. Implement MQTT Listener for Cover Updates

**MQTT Topic**: `users/{user_id}/cover_uploaded`

```swift
import CocoaMQTT

class MQTTManager {
    static let shared = MQTTManager()
    private var mqttClient: CocoaMQTT?

    func subscribeToCoverUpdates(userId: Int) {
        let topic = "users/\(userId)/cover_uploaded"
        mqttClient?.subscribe(topic, qos: .qos1)
    }

    func didReceiveMessage(_ mqtt: CocoaMQTT, message: CocoaMQTTMessage, id: UInt16) {
        guard let payloadString = message.string else { return }

        do {
            let payload = try JSONDecoder().decode(CoverUploadedEvent.self, from: Data(payloadString.utf8))

            // Check if this is an automatic fetch (not manual upload)
            if payload.source == "web_search" {
                print("✅ Book cover automatically fetched for book \(payload.book_id)")
                print("Cover URL: \(payload.cover_url)")

                // Update UI
                DispatchQueue.main.async {
                    self.handleCoverUpdated(bookId: payload.book_id, coverUrl: payload.cover_url)
                }
            }
        } catch {
            print("Error decoding MQTT payload: \(error)")
        }
    }
}

struct CoverUploadedEvent: Codable {
    let book_id: Int
    let cover_url: String
    let timestamp: String
    let source: String  // "web_search" or "manual_upload"
}
```

#### 3. Poll for Cover Updates (Fallback Method)

If MQTT is not available or as a fallback:

```swift
func pollForCoverUpdate(bookId: Int, maxAttempts: Int = 10) {
    var attempts = 0

    Timer.scheduledTimer(withTimeInterval: 2.0, repeats: true) { timer in
        attempts += 1

        APIClient.shared.get("/user/books/\(bookId)") { result in
            switch result {
            case .success(let book):
                if let coverUrl = book.cover_url, !coverUrl.isEmpty {
                    // Cover is ready!
                    print("✅ Book cover ready: \(coverUrl)")
                    timer.invalidate()

                    DispatchQueue.main.async {
                        self.updateBookCover(bookId: bookId, coverUrl: coverUrl)
                    }
                } else if attempts >= maxAttempts {
                    // Timeout: cover fetch may have failed
                    print("⚠️ Cover fetch timeout for book \(bookId)")
                    timer.invalidate()

                    // Optionally: Offer manual upload
                    DispatchQueue.main.async {
                        self.showManualUploadOption(bookId: bookId)
                    }
                }
            case .failure(let error):
                print("Error polling for cover: \(error)")
            }
        }
    }
}
```

#### 4. Update Book List UI to Show Cover Loading State

```swift
struct BookCell: View {
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
                        .aspectRatio(0.625, contentMode: .fit)  // 1000×1600 aspect ratio
                        .frame(width: 60, height: 96)
                } else if isLoadingCover {
                    ProgressView()
                        .frame(width: 60, height: 96)
                } else {
                    Image(systemName: "book.closed")
                        .resizable()
                        .frame(width: 60, height: 96)
                        .foregroundColor(.gray)
                }
            }

            VStack(alignment: .leading) {
                Text(book.title)
                    .font(.headline)
                Text(book.author)
                    .font(.subheadline)
                    .foregroundColor(.secondary)

                if book.cover_url.isEmpty {
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
        guard !book.cover_url.isEmpty else {
            isLoadingCover = true
            return
        }

        // Load cover image from URL
        URLSession.shared.dataTask(with: URL(string: book.cover_url)!) { data, _, _ in
            if let data = data, let image = UIImage(data: data) {
                DispatchQueue.main.async {
                    self.coverImage = image
                    self.isLoadingCover = false
                }
            }
        }.resume()
    }
}
```

#### 5. Fallback to Manual Upload (Optional)

Provide an option for users to manually upload a cover if automatic fetching fails:

```swift
func showManualUploadOption(bookId: Int) {
    let alert = UIAlertController(
        title: "Cover Not Found",
        message: "We couldn't find a cover for this book automatically. Would you like to upload one manually?",
        preferredStyle: .alert
    )

    alert.addAction(UIAlertAction(title: "Upload Cover", style: .default) { _ in
        self.presentImagePicker(for: bookId)
    })

    alert.addAction(UIAlertAction(title: "Skip", style: .cancel))

    present(alert, animated: true)
}

func presentImagePicker(for bookId: Int) {
    let picker = UIImagePickerController()
    picker.delegate = self
    picker.sourceType = .photoLibrary
    picker.tag = bookId  // Store book ID for later use
    present(picker, animated: true)
}

// Upload manually selected cover
func uploadCover(image: UIImage, bookId: Int) {
    guard let imageData = image.jpegData(compressionQuality: 0.8) else { return }

    APIClient.shared.uploadCover(bookId: bookId, imageData: imageData) { result in
        switch result {
        case .success(let response):
            print("✅ Cover uploaded manually: \(response.cover_url)")
        case .failure(let error):
            print("❌ Upload failed: \(error)")
        }
    }
}
```

### Testing Checklist

- [ ] Book creation triggers automatic cover fetching
- [ ] MQTT events received for `cover_uploaded` topic
- [ ] `source` field in MQTT payload correctly identifies "web_search"
- [ ] UI shows loading state while cover is being fetched
- [ ] Cover image displays correctly once fetched
- [ ] Polling fallback works if MQTT is unavailable
- [ ] Manual upload option appears if automatic fetch fails
- [ ] Cover aspect ratio is maintained (0.625 = 1000×1600)
- [ ] Covers load correctly for multiple books in a list
- [ ] Network errors are handled gracefully

### Performance Considerations

**Cover Fetching Time**:
- Typical: 5-15 seconds
- Maximum: 60 seconds (timeout)

**iOS App Recommendations**:
1. **Don't block UI**: Cover fetching is asynchronous, allow users to continue using the app
2. **Show loading indicator**: Display a placeholder or loading spinner while cover is being fetched
3. **Cache covers**: Once fetched, cache the cover image locally for offline access
4. **Handle failures gracefully**: Provide manual upload option if automatic fetch fails
5. **Use MQTT for real-time updates**: More efficient than polling

---

## 📅 Previous Update: October 28, 2025

### Feature: Extended Book Format Support

#### Summary
Added support for additional ebook formats: **MOBI**, **AZW**, and **AZW3**. The backend can now process 6 different book formats for text-to-speech conversion.

#### Supported Book Formats

| Format | File Extension | Status | Implementation | iOS Action Required |
|--------|---------------|--------|----------------|---------------------|
| **PDF** | `.pdf` | ✅ Supported | rsc.io/pdf library | Update file picker |
| **TXT** | `.txt` | ✅ Supported | Native text processing | Update file picker |
| **EPUB** | `.epub` | ✅ Supported | ZIP extraction | Update file picker |
| **MOBI** | `.mobi` | ✅ **NEW** | Calibre ebook-convert | **Add to file picker** |
| **AZW** | `.azw` | ✅ **NEW** | Calibre ebook-convert | **Add to file picker** |
| **AZW3** | `.azw3` | ✅ **NEW** | Calibre ebook-convert | **Add to file picker** |
| **KFX** | `.kfx` | ❌ Not Supported | N/A | Show error message |

---

## 🔄 API Changes

### 1. File Upload Endpoint (Modified)

**Endpoint**: `POST /user/books/upload`

**What Changed**:
- ✅ Now accepts `.mobi`, `.azw`, `.azw3` file extensions
- ✅ Returns more descriptive error messages
- ✅ Explicit KFX format rejection with conversion suggestions

#### Request (No changes to structure)

```http
POST /user/books/upload
Content-Type: multipart/form-data
Authorization: Bearer <jwt_token>

Form Data:
- book_id: string (required)
- file: binary (required)
```

#### Response - Success (No changes)

```json
{
  "message": "File uploaded and split into pages successfully",
  "book_id": "123",
  "total_pages": 45,
  "file_path": "/app/uploads/book.mobi",
  "content_hash": "abc123...",
  "page_indices": 45
}
```

#### Response - New Error Messages

**Invalid Format (Updated)**:
```json
{
  "error": "Invalid file type. Supported formats: PDF, TXT, EPUB, MOBI, AZW, AZW3",
  "note": "KFX format is not supported. Please convert to one of the supported formats first."
}
```

**KFX Format Explicitly Rejected (New)**:
```json
{
  "error": "KFX format is not supported",
  "message": "Please convert your KFX file to EPUB, PDF, MOBI, or AZW3 format first",
  "suggestion": "You can use Calibre or online converters to convert KFX files"
}
```

---

## 📱 iOS Implementation Guide

### Required Changes

#### 1. Update File Picker / Document Browser

**Add New UTI Types** (UniformTypeIdentifiers):

```swift
// Add to your Info.plist or UTType imports
import UniformTypeIdentifiers

// Existing types
.pdf
.plainText
.epub

// NEW: Add these types
.mobi  // org.idpf.epub-container (MOBI uses EPUB container type)
// For AZW/AZW3, you may need custom UTType declarations

// Custom UTType for Kindle formats
extension UTType {
    static let mobi = UTType(filenameExtension: "mobi")!
    static let azw = UTType(filenameExtension: "azw")!
    static let azw3 = UTType(filenameExtension: "azw3")!
}

// Update your document picker
let documentPicker = UIDocumentPickerViewController(
    forOpeningContentTypes: [
        .pdf,
        .plainText,
        .epub,
        .mobi,    // NEW
        .azw,     // NEW
        .azw3     // NEW
    ]
)
```

#### 2. Update File Validation Logic

**Before Upload**:

```swift
func validateBookFile(url: URL) -> (isValid: Bool, error: String?) {
    let fileExtension = url.pathExtension.lowercased()

    let supportedFormats = ["pdf", "txt", "epub", "mobi", "azw", "azw3"] // Updated
    let unsupportedFormats = ["kfx"] // Explicitly blocked

    // Check if KFX format
    if unsupportedFormats.contains(fileExtension) {
        return (false, "KFX format is not supported. Please convert to EPUB, PDF, MOBI, or AZW3 first.")
    }

    // Check if supported
    if !supportedFormats.contains(fileExtension) {
        return (false, "Unsupported file format. Please upload PDF, TXT, EPUB, MOBI, AZW, or AZW3 files.")
    }

    return (true, nil)
}
```

#### 3. Update UI Labels & Help Text

**File Selection Screen**:
```swift
// OLD
"Supported formats: PDF, TXT, EPUB"

// NEW
"Supported formats: PDF, TXT, EPUB, MOBI, AZW, AZW3"
```

**Help/FAQ Section**:
```markdown
Q: What book formats can I upload?
A: You can upload PDF, TXT, EPUB, MOBI, AZW, and AZW3 files.

Q: Can I upload Kindle KFX files?
A: KFX format is not currently supported. Please convert your KFX file to
   EPUB, MOBI, or AZW3 format using Calibre (free tool) before uploading.
```

#### 4. Error Handling Updates

**Handle New Error Responses**:

```swift
func handleUploadError(_ error: APIError) {
    switch error {
    case .invalidFileFormat(let message, let suggestion):
        // Show alert with conversion suggestion
        showAlert(
            title: "Unsupported Format",
            message: message,
            action: suggestion.map { suggestion in
                UIAlertAction(title: "How to Convert", style: .default) { _ in
                    // Open help article or Calibre website
                    openURL("https://calibre-ebook.com/download")
                }
            }
        )
    default:
        // Handle other errors
        break
    }
}
```

#### 5. File Type Icons

Add icons for new formats in your asset catalog:

```
Assets.xcassets/
├── FileIcons/
│   ├── pdf.imageset/
│   ├── txt.imageset/
│   ├── epub.imageset/
│   ├── mobi.imageset/    // NEW
│   ├── azw.imageset/     // NEW
│   └── azw3.imageset/    // NEW
```

**Icon Selection Logic**:

```swift
func iconForFileType(_ fileExtension: String) -> UIImage {
    switch fileExtension.lowercased() {
    case "pdf":
        return UIImage(named: "pdf-icon")!
    case "txt":
        return UIImage(named: "txt-icon")!
    case "epub":
        return UIImage(named: "epub-icon")!
    case "mobi", "azw", "azw3":  // NEW: Use same icon for Kindle formats
        return UIImage(named: "kindle-icon")!
    default:
        return UIImage(named: "file-icon")!
    }
}
```

---

## 🧪 Testing Checklist for iOS

- [ ] File picker shows new format options (MOBI, AZW, AZW3)
- [ ] Can select and upload `.mobi` files successfully
- [ ] Can select and upload `.azw` files successfully
- [ ] Can select and upload `.azw3` files successfully
- [ ] KFX files show appropriate error message
- [ ] Unknown formats show updated error message
- [ ] File type icons display correctly for new formats
- [ ] Help text updated with new supported formats
- [ ] Upload progress works for new formats
- [ ] Error messages from backend are properly displayed
- [ ] Large MOBI/AZW files (>10MB) upload successfully

---

## 📊 Technical Details for iOS Developers

### File Processing Flow

```
iOS App (File Upload)
    ↓
Content Service API (/user/books/upload)
    ↓
File Validation (checks extension)
    ↓
File Storage (/app/uploads/)
    ↓
Text Extraction:
    - PDF → rsc.io/pdf library
    - TXT → Native Go reader
    - EPUB → ZIP extraction + HTML parsing
    - MOBI/AZW/AZW3 → Calibre ebook-convert to TXT
    ↓
Document Chunking (~1000 chars per chunk)
    ↓
Database Storage (book_chunks table)
    ↓
Return Success Response to iOS
```

### Processing Time Estimates

| Format | Avg Processing Time | Notes |
|--------|-------------------|-------|
| TXT | < 1 second | Fastest |
| PDF | 2-5 seconds | Depends on page count |
| EPUB | 3-7 seconds | Depends on size |
| MOBI/AZW/AZW3 | 5-15 seconds | Requires conversion step |

**iOS Recommendation**: Show a progress indicator for MOBI/AZW formats as conversion takes longer.

### File Size Limits

- **Maximum file size**: 50MB (configurable via `MAX_FILE_SIZE` env var)
- **Recommended for mobile upload**: < 20MB
- **iOS should warn users** if uploading files > 20MB over cellular

---

## 🔐 No Authentication Changes

- JWT token authentication remains unchanged
- All endpoints still require valid Bearer token
- Token expiry: 72 hours (unchanged)

---

## 🚀 Deployment Timeline

- **Backend Deployed**: October 28, 2025
- **iOS App Update Recommended**: Before November 15, 2025
- **Breaking Changes**: None (fully backward compatible)
- **Minimum iOS Version**: No changes required

---

## 📝 Sample iOS Implementation

### Complete File Upload with New Formats

```swift
import UIKit
import UniformTypeIdentifiers

class BookUploadViewController: UIViewController {

    // MARK: - File Selection

    func presentDocumentPicker() {
        let supportedTypes: [UTType] = [
            .pdf,
            .plainText,
            .epub,
            UTType(filenameExtension: "mobi")!,
            UTType(filenameExtension: "azw")!,
            UTType(filenameExtension: "azw3")!
        ]

        let picker = UIDocumentPickerViewController(
            forOpeningContentTypes: supportedTypes
        )
        picker.delegate = self
        picker.allowsMultipleSelection = false
        present(picker, animated: true)
    }

    // MARK: - Upload

    func uploadBook(bookId: String, fileURL: URL) {
        // Validate format
        let validation = validateBookFile(url: fileURL)
        guard validation.isValid else {
            showError(validation.error ?? "Invalid file")
            return
        }

        // Show loading for MOBI/AZW formats (slower processing)
        let fileExtension = fileURL.pathExtension.lowercased()
        let isKindleFormat = ["mobi", "azw", "azw3"].contains(fileExtension)

        if isKindleFormat {
            showLoadingMessage("Converting Kindle format... This may take a moment.")
        }

        // Create multipart request
        var request = URLRequest(url: URL(string: "\(baseURL)/user/books/upload")!)
        request.httpMethod = "POST"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

        let boundary = UUID().uuidString
        request.setValue("multipart/form-data; boundary=\(boundary)",
                        forHTTPHeaderField: "Content-Type")

        var body = Data()

        // Add book_id
        body.append("--\(boundary)\r\n")
        body.append("Content-Disposition: form-data; name=\"book_id\"\r\n\r\n")
        body.append("\(bookId)\r\n")

        // Add file
        body.append("--\(boundary)\r\n")
        body.append("Content-Disposition: form-data; name=\"file\"; filename=\"\(fileURL.lastPathComponent)\"\r\n")
        body.append("Content-Type: application/octet-stream\r\n\r\n")
        body.append(try! Data(contentsOf: fileURL))
        body.append("\r\n")
        body.append("--\(boundary)--\r\n")

        request.httpBody = body

        // Send request
        URLSession.shared.dataTask(with: request) { data, response, error in
            DispatchQueue.main.async {
                self.hideLoading()
                self.handleUploadResponse(data: data, response: response, error: error)
            }
        }.resume()
    }

    // MARK: - Validation

    func validateBookFile(url: URL) -> (isValid: Bool, error: String?) {
        let fileExtension = url.pathExtension.lowercased()

        let supportedFormats = ["pdf", "txt", "epub", "mobi", "azw", "azw3"]
        let unsupportedFormats = ["kfx"]

        if unsupportedFormats.contains(fileExtension) {
            return (false, """
                KFX format is not supported.

                Please convert to EPUB, PDF, MOBI, or AZW3 first.
                You can use Calibre (free) to convert KFX files.
                """)
        }

        if !supportedFormats.contains(fileExtension) {
            return (false, """
                Unsupported file format: .\(fileExtension)

                Supported formats: PDF, TXT, EPUB, MOBI, AZW, AZW3
                """)
        }

        return (true, nil)
    }

    // MARK: - Response Handling

    func handleUploadResponse(data: Data?, response: URLResponse?, error: Error?) {
        guard let data = data else {
            showError("Network error: \(error?.localizedDescription ?? "Unknown")")
            return
        }

        do {
            let json = try JSONDecoder().decode(UploadResponse.self, from: data)

            if let errorMessage = json.error {
                // Handle specific error for KFX
                if errorMessage.contains("KFX") {
                    showKFXConversionHelp(
                        message: json.message,
                        suggestion: json.suggestion
                    )
                } else {
                    showError(errorMessage)
                }
            } else {
                // Success
                onUploadSuccess(
                    bookId: json.book_id,
                    totalPages: json.total_pages
                )
            }
        } catch {
            showError("Failed to parse response")
        }
    }

    func showKFXConversionHelp(message: String?, suggestion: String?) {
        let alert = UIAlertController(
            title: "Format Not Supported",
            message: message ?? "KFX format cannot be processed.",
            preferredStyle: .alert
        )

        alert.addAction(UIAlertAction(title: "Learn More", style: .default) { _ in
            // Open Calibre download page
            if let url = URL(string: "https://calibre-ebook.com/download") {
                UIApplication.shared.open(url)
            }
        })

        alert.addAction(UIAlertAction(title: "OK", style: .cancel))

        present(alert, animated: true)
    }
}

// MARK: - Models

struct UploadResponse: Codable {
    let message: String?
    let book_id: String?
    let total_pages: Int?
    let file_path: String?
    let content_hash: String?
    let page_indices: Int?

    // Error fields
    let error: String?
    let note: String?
    let suggestion: String?
}

// MARK: - Extensions

extension Data {
    mutating func append(_ string: String) {
        if let data = string.data(using: .utf8) {
            append(data)
        }
    }
}
```

---

## 🎯 Quick Migration Checklist

For iOS developers implementing this update:

1. **File Picker** (5 min)
   - [ ] Add `.mobi`, `.azw`, `.azw3` to UTType array
   - [ ] Test file selection

2. **UI Updates** (10 min)
   - [ ] Update "Supported formats" text in 3 places
   - [ ] Add Kindle format icons
   - [ ] Update FAQ/Help section

3. **Validation** (10 min)
   - [ ] Add new formats to validation array
   - [ ] Add KFX rejection logic
   - [ ] Test with sample files

4. **Error Handling** (15 min)
   - [ ] Add KFX error alert with Calibre link
   - [ ] Update generic format error message
   - [ ] Test error scenarios

5. **Testing** (30 min)
   - [ ] Upload test files for each new format
   - [ ] Verify processing completes
   - [ ] Test error cases

**Total Estimated Time**: ~70 minutes

---

## 📞 Support & Questions

**For Backend API Issues**:
- Check backend logs: `docker compose logs content-service`
- Health check: `GET /user/books/:id/chunks/pages`

**For iOS Integration Help**:
- Reference this document
- Test with sample files: [Sample MOBI/AZW files](https://calibre-ebook.com/downloads)

---

## 🔄 Version History

| Version | Date | Changes | iOS Action |
|---------|------|---------|------------|
| **2.0.0** | Oct 28, 2025 | Added MOBI, AZW, AZW3 support | Update file picker |
| 1.0.0 | Aug 4, 2025 | Initial release (PDF, TXT, EPUB) | N/A |

---

## 📋 Next Planned Updates

*To be announced - check this file for future updates*

Possible future features:
- [ ] DOCX format support
- [ ] RTF format support
- [ ] Direct KFX support (if library becomes available)
- [ ] Batch upload API
- [ ] Progress webhooks for long conversions

---

**End of Changelog**

*Last updated: October 28, 2025*
*Backend Version: 2.0.0*
*API Base URL: https://your-server.com or http://localhost:8083*
