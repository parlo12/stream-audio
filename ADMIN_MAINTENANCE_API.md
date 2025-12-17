# Admin Maintenance API Documentation

## Overview

The Admin Maintenance API provides powerful tools for database and file system management. These endpoints allow administrators to clean up user data, wipe the entire system, and manage individual user files.

**⚠️ WARNING:** These endpoints are DESTRUCTIVE and cannot be undone. Use with extreme caution.

**Base URL:** `http://68.183.22.205:8080/api` (Gateway routes to Auth/Content Services)

---

## Authentication

All maintenance endpoints require admin authentication:

**Headers:**
```
Authorization: Bearer {admin_jwt_token}
```

**Requirements:**
- Valid JWT token with `is_admin: true`
- Admin users cannot delete other admin users or themselves

---

## Endpoints

### 1. Complete System Wipe

**POST** `/admin/system/wipe`

Wipes all non-admin users and their database records from the system. Admin accounts are preserved.

**⚠️ EXTREME CAUTION:** This operation deletes ALL user data except admin accounts!

**Request Body:**
```json
{
  "confirmation_token": "WIPE_ALL_USER_DATA_CONFIRM"
}
```

**Response (200 OK):**
```json
{
  "message": "System wiped successfully",
  "users_deleted": 125,
  "admins_preserved": 2,
  "wiped_by_admin": 5
}
```

**Error Responses:**
- `400 Bad Request` - Missing confirmation_token
- `403 Forbidden` - Invalid confirmation token
- `500 Internal Server Error` - Database error

**Notes:**
- Requires exact confirmation token: `WIPE_ALL_USER_DATA_CONFIRM`
- Deletes users, user_histories, and user_book_histories
- Preserves all admin accounts
- Uses database transactions for consistency
- Logs admin user ID who initiated the wipe

**Example:**
```bash
curl -X POST http://68.183.22.205:8080/api/admin/system/wipe \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"confirmation_token": "WIPE_ALL_USER_DATA_CONFIRM"}'
```

---

### 2. Delete User Files

**DELETE** `/admin/users/:user_id/files`

Deletes all files and database records for a specific user from the content service.

**URL Parameters:**
- `user_id` (required) - The user ID to delete files for

**Response (200 OK):**
```json
{
  "message": "User files deleted successfully",
  "user_id": 42,
  "books_deleted": 15,
  "chunks_deleted": 350,
  "uploads_deleted": 15,
  "audio_deleted": 15,
  "covers_deleted": 12,
  "chunk_files_deleted": 350
}
```

**What Gets Deleted:**

**Files:**
- Book upload files (PDF/TXT/EPUB/MOBI)
- Book audio files (TTS generated MP3s)
- Book cover images
- Chunk audio files (segment MP3s)
- Audio segment directories

**Database Records:**
- Books
- Book chunks
- Processed chunk groups
- TTS queue jobs
- Playback progress

**Error Responses:**
- `400 Bad Request` - Invalid user_id
- `404 Not Found` - User not found
- `500 Internal Server Error` - Deletion failed

**Example:**
```bash
curl -X DELETE http://68.183.22.205:8080/api/admin/users/42/files \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

---

### 3. Delete User Data (Database Only)

**DELETE** `/admin/users/:user_id/data`

Deletes only database records for a user from the auth service (does NOT delete files).

**URL Parameters:**
- `user_id` (required) - The user ID to delete data for

**Response (200 OK):**
```json
{
  "message": "User data deleted successfully",
  "user_id": 42,
  "username": "john_doe",
  "email": "john@example.com"
}
```

**What Gets Deleted:**

**Database Records:**
- User account
- User histories
- User book histories

**What DOES NOT Get Deleted:**
- Files (uploads, audio, covers)
- Use `/admin/users/:user_id/complete` to delete files + data

**Error Responses:**
- `400 Bad Request` - Invalid user_id
- `403 Forbidden` - Cannot delete admin user
- `404 Not Found` - User not found
- `500 Internal Server Error` - Deletion failed

**Example:**
```bash
curl -X DELETE http://68.183.22.205:8080/api/admin/users/42/data \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

---

### 4. Complete User Deletion (Files + Data)

**DELETE** `/admin/users/:user_id/complete`

Completely deletes a user: files from content service + database records from auth service.

**URL Parameters:**
- `user_id` (required) - The user ID to completely delete

**Response (200 OK):**
```json
{
  "message": "User completely deleted (files + database)",
  "user_id": 42,
  "username": "john_doe",
  "email": "john@example.com"
}
```

**What Gets Deleted:**

**Everything:**
- All files (uploads, audio, covers, chunks)
- All database records (user, histories, books, chunks, progress)
- Complete removal from both auth and content services

**Error Responses:**
- `400 Bad Request` - Invalid user_id
- `403 Forbidden` - Cannot delete admin user
- `404 Not Found` - User not found
- `500 Internal Server Error` - Deletion failed

**Notes:**
- Attempts to delete files first, then database records
- If file deletion fails, continues with database deletion (logged as warning)
- Cannot delete admin users

**Example:**
```bash
curl -X DELETE http://68.183.22.205:8080/api/admin/users/42/complete \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

---

## Comparison of Deletion Endpoints

| Endpoint | Files Deleted | Database Deleted | Can Delete Admins | Use Case |
|----------|---------------|------------------|-------------------|----------|
| `/system/wipe` | ❌ No | ✅ All non-admin users | ❌ No | Fresh start, development reset |
| `/users/:id/files` | ✅ Yes | ✅ Yes (content-service) | ❌ No | Remove user's content only |
| `/users/:id/data` | ❌ No | ✅ Yes (auth-service) | ❌ No | Remove account, keep files |
| `/users/:id/complete` | ✅ Yes | ✅ Yes (both services) | ❌ No | Complete user removal |

---

## Safety Features

### 1. Admin Protection
- Admin users cannot delete other admin users
- Admin users cannot delete themselves
- System wipe preserves all admin accounts

### 2. Confirmation Requirements
- System wipe requires exact confirmation token
- Token must be: `WIPE_ALL_USER_DATA_CONFIRM`

### 3. Transaction Safety
- All database operations use transactions
- Rollback on any error
- Atomic operations where possible

### 4. Comprehensive Logging
- All deletions logged with admin user ID
- Emoji indicators for easy log searching:
  - 🚨 System wipe initiated
  - 🗑️ Individual deletions
  - ✅ Successful operations
  - ⚠️ Warnings

**Log Examples:**
```
🚨 SYSTEM WIPE initiated by admin user ID 5
✅ SYSTEM WIPE completed: 125 users deleted, 2 admin accounts preserved
🗑️ Files deleted for user ID 42 by admin
🗑️ Complete deletion (files + data) for user ID 42 (john_doe) by admin
⚠️ Warning: Failed to delete files for user 42: connection timeout
```

---

## Error Handling

All endpoints return standard error responses:

**400 Bad Request:**
```json
{
  "error": "Invalid user_id"
}
```

**403 Forbidden:**
```json
{
  "error": "Cannot delete admin user"
}
```

**404 Not Found:**
```json
{
  "error": "User not found"
}
```

**500 Internal Server Error:**
```json
{
  "error": "Failed to delete user",
  "details": "database connection timeout"
}
```

---

## Workflow Examples

### Example 1: Clean Up Test Users

```bash
# Get list of test users
curl -X GET http://68.183.22.205:8080/api/admin/users?search=test \
  -H "Authorization: Bearer $ADMIN_TOKEN"

# Delete each test user completely
curl -X DELETE http://68.183.22.205:8080/api/admin/users/42/complete \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### Example 2: Reset Development Environment

```bash
# Wipe all non-admin users
curl -X POST http://68.183.22.205:8080/api/admin/system/wipe \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"confirmation_token": "WIPE_ALL_USER_DATA_CONFIRM"}'
```

### Example 3: Remove User Content Only

```bash
# Delete files but keep account (user can upload new books)
curl -X DELETE http://68.183.22.205:8080/api/admin/users/42/files \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### Example 4: Remove Account Only

```bash
# Delete account but keep files (for data migration)
curl -X DELETE http://68.183.22.205:8080/api/admin/users/42/data \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

---

## Best Practices

### 1. Always Verify Before Deletion
```bash
# Get user details first
curl -X GET http://68.183.22.205:8080/api/admin/users \
  -H "Authorization: Bearer $ADMIN_TOKEN"

# Check their books
curl -X GET http://68.183.22.205:8080/api/admin/users/42 \
  -H "Authorization: Bearer $ADMIN_TOKEN"

# Then delete
curl -X DELETE http://68.183.22.205:8080/api/admin/users/42/complete \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### 2. Monitor Logs
```bash
# Watch logs during deletion
docker logs -f stream-audio-auth-service-1 | grep "🗑️"
docker logs -f stream-audio-content-service-1 | grep "🗑️"
```

### 3. Backup Before System Wipe
```bash
# Backup database first
pg_dump -h your-db-host -U user -d streaming_db > backup_$(date +%Y%m%d).sql

# Then wipe
curl -X POST http://68.183.22.205:8080/api/admin/system/wipe \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"confirmation_token": "WIPE_ALL_USER_DATA_CONFIRM"}'
```

---

## Monitoring & Audit Trail

### Database Queries

**Check total users:**
```sql
SELECT COUNT(*) FROM users WHERE is_admin = false;
```

**Check admin users:**
```sql
SELECT id, username, email, is_admin FROM users WHERE is_admin = true;
```

**Check user files:**
```sql
SELECT
  u.id,
  u.username,
  COUNT(b.id) as total_books,
  COUNT(bc.id) as total_chunks
FROM users u
LEFT JOIN books b ON b.user_id = u.id
LEFT JOIN book_chunks bc ON bc.book_id = b.id
WHERE u.is_admin = false
GROUP BY u.id, u.username;
```

### Log Monitoring

**Watch deletion logs:**
```bash
# Auth service
docker logs -f stream-audio-auth-service-1 | grep -E "🚨|🗑️|✅"

# Content service
docker logs -f stream-audio-content-service-1 | grep "🗑️"
```

---

## Rollback & Recovery

⚠️ **These operations CANNOT be undone!**

### If You Accidentally Delete a User:

1. **Restore from database backup:**
   ```bash
   psql -h your-db-host -U user -d streaming_db < backup.sql
   ```

2. **Contact the user:**
   - Their account and files are gone
   - They need to re-register and re-upload books

### If You Accidentally Wipe System:

1. **Stop all services immediately:**
   ```bash
   docker-compose down
   ```

2. **Restore database:**
   ```bash
   psql -h your-db-host -U user -d streaming_db < backup.sql
   ```

3. **Restart services:**
   ```bash
   docker-compose up -d
   ```

---

## Security Considerations

### Access Control
- Only admin users can access these endpoints
- JWT token must have `is_admin: true`
- Gateway routes all requests through auth middleware

### Rate Limiting
Consider adding rate limiting for these endpoints:
```nginx
location /api/admin/system/wipe {
    limit_req zone=admin burst=1 nodelay;
    proxy_pass http://auth-service:8082;
}
```

### IP Whitelisting
Consider restricting to specific IP addresses:
```nginx
location /api/admin {
    allow 192.168.1.0/24;
    deny all;
    proxy_pass http://auth-service:8082;
}
```

---

## Deployment Notes

### Environment Variables

No additional environment variables required. Uses existing:
- `JWT_SECRET` - For token validation
- `CONTENT_SERVICE_URL` - For forwarding file deletion requests (default: `http://content-service:8083`)

### Service Dependencies

- Auth service communicates with content service via HTTP
- Both services must be running for complete deletion to work
- If content service is down, file deletion will fail but continue

---

## Summary

✅ **Features:**
- Complete system wipe with admin preservation
- Individual user file deletion
- Individual user data deletion
- Complete user deletion (files + data)
- Transaction safety
- Comprehensive logging
- Admin protection

⚠️ **Warnings:**
- All operations are DESTRUCTIVE
- NO undo/rollback functionality
- Admin users cannot be deleted
- Requires proper authentication

📝 **Best Practices:**
- Always backup before system wipe
- Verify user details before deletion
- Monitor logs during operations
- Use appropriate endpoint for use case

**Deployment Status:** ✅ Ready for production

**Date:** December 17, 2025
**Commit:** `379ae88`
