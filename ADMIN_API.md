# Admin API Documentation

## Overview

The Admin API provides comprehensive analytics and user management capabilities for platform administrators. All admin endpoints require authentication with an admin account.

**Base URL:** `http://68.183.22.205:8080` (Gateway routes to Auth Service)

---

## Authentication

All admin endpoints require:
1. **JWT token** - User must be authenticated
2. **Admin privileges** - User must have `is_admin = true` in the database

### Headers
```
Authorization: Bearer {admin_jwt_token}
```

### Response Codes
- `200 OK` - Success
- `401 Unauthorized` - No valid JWT token
- `403 Forbidden` - User is not an admin
- `500 Internal Server Error` - Server error

---

## User Activity Tracking

### Update User Activity

**Endpoint:** `POST /user/activity/ping`

**Description:** Updates the user's `last_active_at` timestamp. This should be called periodically from the iOS app (e.g., every 5 minutes when app is in foreground) to track active users.

**Headers:**
```
Authorization: Bearer {jwt_token}
```

**Response (200 OK):**
```json
{
  "message": "Activity updated"
}
```

**iOS Example:**
```swift
func pingUserActivity() {
    guard let token = KeychainSwift().get("auth_token") else { return }

    let url = URL(string: "http://68.183.22.205:8080/user/activity/ping")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    URLSession.shared.dataTask(with: request) { _, _, _ in
        // Activity pinged
    }.resume()
}

// Call periodically when app is active
Timer.scheduledTimer(withTimeInterval: 300, repeats: true) { _ in
    pingUserActivity()
}
```

---

## Admin Statistics

### Get Platform Stats

**Endpoint:** `GET /admin/stats`

**Description:** Returns overall platform statistics including user counts, active users, and growth metrics.

**Headers:**
```
Authorization: Bearer {admin_jwt_token}
```

**Response (200 OK):**
```json
{
  "total_users": 1523,
  "paid_users": 342,
  "free_users": 1181,
  "active_users_7d": 856,
  "new_users_today": 15,
  "new_users_this_week": 127
}
```

**Response Fields:**
- `total_users` - Total registered users
- `paid_users` - Users with `account_type = "paid"`
- `free_users` - Users with `account_type = "free"`
- `active_users_7d` - Users active in the last 7 days
- `new_users_today` - Users created today (since midnight UTC)
- `new_users_this_week` - Users created in the last 7 days

**cURL Example:**
```bash
curl -X GET http://68.183.22.205:8080/admin/stats \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN"
```

---

## User Management

### List All Users

**Endpoint:** `GET /admin/users`

**Description:** Returns a paginated list of all users with filtering and search capabilities.

**Headers:**
```
Authorization: Bearer {admin_jwt_token}
```

**Query Parameters:**
- `page` (optional, default: 1) - Page number
- `limit` (optional, default: 50, max: 200) - Results per page
- `account_type` (optional) - Filter by "free" or "paid"
- `is_admin` (optional) - Filter by admin status ("true")
- `search` (optional) - Search by username or email (case-insensitive)

**Examples:**
```
GET /admin/users
GET /admin/users?page=2&limit=100
GET /admin/users?account_type=paid
GET /admin/users?search=john
GET /admin/users?account_type=free&page=1&limit=20
```

**Response (200 OK):**
```json
{
  "users": [
    {
      "ID": 123,
      "Username": "johndoe",
      "Email": "john@example.com",
      "AccountType": "paid",
      "IsAdmin": false,
      "IsPublic": true,
      "State": "California",
      "StripeCustomerID": "cus_ABC123",
      "BooksRead": 15,
      "LastActiveAt": "2025-12-16T10:30:00Z",
      "CreatedAt": "2025-01-15T08:20:00Z",
      "UpdatedAt": "2025-12-16T10:30:00Z"
    },
    // ... more users
  ],
  "total": 1523,
  "page": 1,
  "limit": 50,
  "total_pages": 31
}
```

**cURL Example:**
```bash
# Get paid users
curl -X GET "http://68.183.22.205:8080/admin/users?account_type=paid" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN"

# Search for users
curl -X GET "http://68.183.22.205:8080/admin/users?search=john" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN"
```

---

## Active Users Tracking

### Get Active Users

**Endpoint:** `GET /admin/users/active`

**Description:** Returns users who have been active within the specified time period, with detailed activity metrics.

**Headers:**
```
Authorization: Bearer {admin_jwt_token}
```

**Query Parameters:**
- `days` (optional, default: 7) - Number of days to look back

**Examples:**
```
GET /admin/users/active           # Last 7 days (default)
GET /admin/users/active?days=30   # Last 30 days
GET /admin/users/active?days=1    # Last 24 hours
```

**Response (200 OK):**
```json
{
  "active_users": [
    {
      "id": 123,
      "username": "johndoe",
      "email": "john@example.com",
      "account_type": "paid",
      "last_active_at": "2025-12-16T10:30:00Z",
      "days_active": 0,
      "books_read": 15
    },
    {
      "id": 124,
      "username": "janedoe",
      "email": "jane@example.com",
      "account_type": "free",
      "last_active_at": "2025-12-15T14:20:00Z",
      "days_active": 1,
      "books_read": 3
    }
  ],
  "total_active": 856,
  "weekly_active_count": 856,
  "daily_active_count": 203,
  "days_filter": 7
}
```

**Response Fields:**
- `active_users` - Array of active users sorted by most recent activity
- `days_active` - Days since last activity (0 = active today)
- `total_active` - Count of active users in the period
- `weekly_active_count` - Users active in last 7 days
- `daily_active_count` - Users active in last 24 hours
- `days_filter` - The filter applied (from query param)

**cURL Example:**
```bash
# Get users active in last 30 days
curl -X GET "http://68.183.22.205:8080/admin/users/active?days=30" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN"
```

---

## Admin Management

### Make User Admin

**Endpoint:** `POST /admin/users/:user_id/admin`

**Description:** Grant or revoke admin privileges for a user.

**Headers:**
```
Authorization: Bearer {admin_jwt_token}
Content-Type: application/json
```

**URL Parameters:**
- `user_id` - The ID of the user to modify

**Request Body:**
```json
{
  "is_admin": true
}
```

**Request Fields:**
- `is_admin` (required) - `true` to grant admin, `false` to revoke

**Response (200 OK):**
```json
{
  "message": "Admin access granted successfully",
  "user_id": "123",
  "is_admin": true
}
```

**cURL Examples:**
```bash
# Grant admin access
curl -X POST http://68.183.22.205:8080/admin/users/123/admin \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"is_admin": true}'

# Revoke admin access
curl -X POST http://68.183.22.205:8080/admin/users/123/admin \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"is_admin": false}'
```

---

## Creating Your First Admin User

Since admin access is required to use these endpoints, you need to manually set the first admin user in the database:

### Option 1: Using PostgreSQL CLI

```bash
# Connect to your database
psql -h private-streaming-db-do-user-15814952-0.k.db.ondigitalocean.com \
     -U doadmin \
     -d defaultdb \
     -p 25060

# Make a user admin
UPDATE users SET is_admin = true WHERE email = 'your-admin@example.com';

# Verify
SELECT id, username, email, is_admin FROM users WHERE is_admin = true;
```

### Option 2: Using Docker Exec

```bash
# SSH to your server
ssh root@68.183.22.205

# Connect to PostgreSQL container (if running locally)
docker exec -it postgres-container psql -U doadmin -d defaultdb

# Run the same UPDATE query
UPDATE users SET is_admin = true WHERE email = 'your-admin@example.com';
```

### Option 3: Via API Script

```bash
# First, login as the user you want to make admin
TOKEN=$(curl -X POST http://68.183.22.205:8080/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"your_password"}' \
  | jq -r '.token')

# Manually update database as shown in Option 1 or 2

# Then test admin access
curl -X GET http://68.183.22.205:8080/admin/stats \
  -H "Authorization: Bearer $TOKEN"
```

---

## Common Use Cases

### Monitor Platform Growth

```bash
# Get overall stats
curl -X GET http://68.183.22.205:8080/admin/stats \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### Find Inactive Paid Users

```bash
# Get all paid users
curl -X GET "http://68.183.22.205:8080/admin/users?account_type=paid" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  | jq '.users[] | select(.LastActiveAt < "2025-11-01")'
```

### Track Daily Active Users

```bash
# Get users active today
curl -X GET "http://68.183.22.205:8080/admin/users/active?days=1" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  | jq '.daily_active_count'
```

### Search for User by Email

```bash
curl -X GET "http://68.183.22.205:8080/admin/users?search=john@example.com" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

### Weekly Active Users Report

```bash
curl -X GET "http://68.183.22.205:8080/admin/users/active?days=7" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  | jq '{
      total_active: .total_active,
      daily_active: .daily_active_count,
      weekly_active: .weekly_active_count,
      retention_rate: (.daily_active_count / .total_active * 100)
    }'
```

---

## Error Handling

### 401 Unauthorized
```json
{
  "error": "Unauthorized"
}
```
**Cause:** No JWT token provided or token is invalid

### 403 Forbidden
```json
{
  "error": "Admin access required"
}
```
**Cause:** User is authenticated but doesn't have `is_admin = true`

### 500 Internal Server Error
```json
{
  "error": "Failed to fetch users"
}
```
**Cause:** Database error or server issue

---

## Database Schema Changes

The following fields were added to the `users` table:

```sql
ALTER TABLE users
ADD COLUMN is_admin BOOLEAN DEFAULT false,
ADD COLUMN last_active_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;

-- Create index for performance
CREATE INDEX idx_users_last_active ON users(last_active_at);
CREATE INDEX idx_users_is_admin ON users(is_admin) WHERE is_admin = true;
```

These migrations are automatically applied by GORM when the auth service starts.

---

## Security Considerations

1. **Admin Token Protection**: Store admin tokens securely. Never commit them to code repositories.

2. **HTTPS Recommended**: In production, use HTTPS instead of HTTP for all admin endpoints.

3. **Rate Limiting**: Consider adding rate limiting to admin endpoints to prevent abuse.

4. **Audit Logging**: All admin actions are logged with the admin's user ID.

5. **IP Whitelisting**: Consider restricting admin endpoints to specific IP addresses.

---

## Deployment

After pulling the latest code with admin features:

```bash
# SSH to server
ssh root@68.183.22.205

# Pull latest code
cd /opt/stream-audio/stream-audio
git pull origin main

# Rebuild auth service
docker-compose -f docker-compose.prod.yml up -d --build auth-service

# Verify new endpoints are registered
docker-compose -f docker-compose.prod.yml logs auth-service | grep admin

# Expected output:
# → GET /admin/stats
# → GET /admin/users
# → GET /admin/users/active
# → POST /admin/users/:user_id/admin
# → POST /user/activity/ping
```

---

## Integration with Monitoring Tools

### Prometheus Metrics (Future Enhancement)

```go
// Example metrics to add
var (
    totalUsersGauge = prometheus.NewGauge(...)
    paidUsersGauge = prometheus.NewGauge(...)
    activeUsersGauge = prometheus.NewGauge(...)
)

// Periodically update
func updateMetrics() {
    stats := getAdminStats()
    totalUsersGauge.Set(float64(stats.TotalUsers))
    // ...
}
```

### Grafana Dashboard

Create dashboards using the `/admin/stats` endpoint:
- Total Users Over Time
- Paid vs Free Users Ratio
- Daily/Weekly Active Users
- User Growth Rate
- Conversion Rate (Free → Paid)

---

## Summary

The Admin API provides:

✅ **User Management**
- List all users with pagination
- Search by username/email
- Filter by account type
- Grant/revoke admin access

✅ **Analytics**
- Platform statistics (users, subscriptions)
- Active user tracking
- Growth metrics (new users)
- Activity duration tracking

✅ **Activity Monitoring**
- Track when users are active
- Identify inactive users
- Monitor engagement

All endpoints are protected by admin authentication and ready for production use!
