# Social Login Backend Implementation Specifications

**For Backend Team**: Implement these endpoints to enable social login in the iOS app.

---

## Overview

The iOS app needs backend endpoints to handle social authentication. The flow is:
1. User taps "Continue with [Provider]" in app
2. App authenticates with provider and receives an ID token/access token
3. App sends token to your backend
4. Backend verifies token with provider
5. Backend creates/finds user and returns JWT auth token

---

## Required Endpoints

### 1. POST /auth/apple

**Sign in with Apple**

Apple provides an `identityToken` (JWT) that your backend must verify.

**Request:**
```json
{
  "identity_token": "eyJraWQ...",  // JWT from Apple
  "user_identifier": "001234.abcd...",  // Apple user ID
  "email": "user@privaterelay.appleid.com",  // May be null after first login
  "full_name": {
    "given_name": "John",
    "family_name": "Doe"
  }  // Only provided on FIRST sign-in, null afterwards
}
```

**Backend Verification:**
```python
# Python example using PyJWT
import jwt
from jwt import PyJWKClient

def verify_apple_token(identity_token):
    # Apple's public keys endpoint
    jwks_client = PyJWKClient("https://appleid.apple.com/auth/keys")
    signing_key = jwks_client.get_signing_key_from_jwt(identity_token)

    decoded = jwt.decode(
        identity_token,
        signing_key.key,
        algorithms=["RS256"],
        audience="YOUR_APP_BUNDLE_ID",  # e.g., "com.narrafied.audiobook"
        issuer="https://appleid.apple.com"
    )

    return decoded  # Contains 'sub' (user_identifier), 'email', etc.
```

**Response (Success):**
```json
{
  "token": "your_jwt_token_here",
  "user": {
    "id": 123,
    "username": "john_doe",
    "email": "user@privaterelay.appleid.com",
    "account_type": "free",
    "is_new_user": true
  }
}
```

**Response (Error):**
```json
{
  "error": "invalid_token",
  "message": "Apple identity token verification failed"
}
```

---

### 2. POST /auth/google

**Sign in with Google**

Google provides an `idToken` that your backend must verify.

**Request:**
```json
{
  "id_token": "eyJhbG...",  // JWT from Google
  "access_token": "ya29..."  // Optional, for additional API calls
}
```

**Backend Verification:**
```python
# Python example using google-auth
from google.oauth2 import id_token
from google.auth.transport import requests

def verify_google_token(token):
    # Your Google OAuth Client ID
    CLIENT_ID = "YOUR_GOOGLE_CLIENT_ID.apps.googleusercontent.com"

    idinfo = id_token.verify_oauth2_token(
        token,
        requests.Request(),
        CLIENT_ID
    )

    # idinfo contains: 'sub', 'email', 'name', 'picture', etc.
    return idinfo
```

**Response (Success):**
```json
{
  "token": "your_jwt_token_here",
  "user": {
    "id": 123,
    "username": "john_doe",
    "email": "john@gmail.com",
    "account_type": "free",
    "is_new_user": false,
    "profile_picture": "https://..."
  }
}
```

---

### 3. POST /auth/facebook

**Sign in with Facebook**

Facebook provides an `accessToken` that your backend must verify via Facebook's Graph API.

**Request:**
```json
{
  "access_token": "EAABsbCS...",  // Access token from Facebook
  "user_id": "1234567890"  // Facebook user ID
}
```

**Backend Verification:**
```python
# Python example
import requests

def verify_facebook_token(access_token, user_id):
    # Verify token and get user info
    url = f"https://graph.facebook.com/me?fields=id,name,email,picture&access_token={access_token}"

    response = requests.get(url)
    data = response.json()

    if 'error' in data:
        raise Exception(data['error']['message'])

    # Verify user_id matches
    if data['id'] != user_id:
        raise Exception("User ID mismatch")

    return data  # Contains 'id', 'name', 'email', 'picture'
```

**Response (Success):**
```json
{
  "token": "your_jwt_token_here",
  "user": {
    "id": 123,
    "username": "john_doe",
    "email": "john@facebook.com",
    "account_type": "free",
    "is_new_user": true,
    "profile_picture": "https://..."
  }
}
```

---

## Database Schema Updates

Add social login support to your users table:

```sql
ALTER TABLE users ADD COLUMN apple_user_id VARCHAR(255) UNIQUE;
ALTER TABLE users ADD COLUMN google_user_id VARCHAR(255) UNIQUE;
ALTER TABLE users ADD COLUMN facebook_user_id VARCHAR(255) UNIQUE;
ALTER TABLE users ADD COLUMN auth_provider ENUM('email', 'apple', 'google', 'facebook') DEFAULT 'email';
ALTER TABLE users ADD COLUMN profile_picture_url TEXT;

-- Index for faster lookups
CREATE INDEX idx_users_apple_id ON users(apple_user_id);
CREATE INDEX idx_users_google_id ON users(google_user_id);
CREATE INDEX idx_users_facebook_id ON users(facebook_user_id);
```

---

## User Creation/Lookup Logic

```python
def handle_social_login(provider, provider_user_id, email, name, profile_picture=None):
    # 1. Check if user exists by provider ID
    user = find_user_by_provider(provider, provider_user_id)

    if user:
        # Existing user - update last login
        user.update_last_login()
        return user, is_new_user=False

    # 2. Check if user exists by email (linking accounts)
    if email:
        user = find_user_by_email(email)
        if user:
            # Link social account to existing user
            user.set_provider_id(provider, provider_user_id)
            return user, is_new_user=False

    # 3. Create new user
    username = generate_unique_username(name or email)
    user = create_user(
        username=username,
        email=email,
        auth_provider=provider,
        **{f"{provider}_user_id": provider_user_id},
        profile_picture_url=profile_picture
    )

    return user, is_new_user=True
```

---

## Important Notes

### Apple Sign In Specifics

1. **Email is only provided once**: Apple only sends the user's email on the FIRST sign-in. Store it immediately!

2. **Private Relay Email**: Users can hide their real email. You'll get something like `abc123@privaterelay.appleid.com`

3. **Name is only provided once**: Like email, the user's name is only sent on first authorization.

4. **User can revoke access**: Users can revoke app access in Settings > Apple ID > Sign-In & Security > Apps Using Apple ID

### Google Sign In Specifics

1. **Client ID**: You need a Google OAuth Client ID from Google Cloud Console

2. **iOS Client ID**: Create an iOS OAuth client specifically for your app

3. **Bundle ID**: Must match your iOS app's bundle identifier

### Facebook Login Specifics

1. **App ID & Secret**: Get from Facebook Developer Console

2. **Email Permission**: Must request `email` permission to get user's email

3. **App Review**: For production, Facebook requires app review for certain permissions

---

## Environment Variables Needed

```bash
# Apple
APPLE_TEAM_ID=YOUR_TEAM_ID
APPLE_KEY_ID=YOUR_KEY_ID
APPLE_PRIVATE_KEY=-----BEGIN PRIVATE KEY-----...
APPLE_BUNDLE_ID=com.narrafied.audiobook

# Google
GOOGLE_CLIENT_ID=YOUR_CLIENT_ID.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=YOUR_CLIENT_SECRET

# Facebook
FACEBOOK_APP_ID=YOUR_APP_ID
FACEBOOK_APP_SECRET=YOUR_APP_SECRET
```

---

## Testing

### Test Apple Sign In
```bash
curl -X POST https://narrafied.com/auth/apple \
  -H "Content-Type: application/json" \
  -d '{
    "identity_token": "test_token",
    "user_identifier": "001234.test",
    "email": "test@privaterelay.appleid.com"
  }'
```

### Test Google Sign In
```bash
curl -X POST https://narrafied.com/auth/google \
  -H "Content-Type: application/json" \
  -d '{
    "id_token": "test_google_token"
  }'
```

### Test Facebook Login
```bash
curl -X POST https://narrafied.com/auth/facebook \
  -H "Content-Type: application/json" \
  -d '{
    "access_token": "test_fb_token",
    "user_id": "1234567890"
  }'
```

---

## Error Response Format

All endpoints should return consistent error responses:

```json
{
  "error": "error_code",
  "message": "Human readable message"
}
```

**Error Codes:**
- `invalid_token` - Token verification failed
- `token_expired` - Token has expired
- `user_not_found` - User doesn't exist (shouldn't happen with social login)
- `account_disabled` - User account is deactivated
- `account_deleted` - User account was deleted
- `server_error` - Internal server error

---

## Timeline Estimate

| Task | Effort |
|------|--------|
| Database schema updates | 1 hour |
| Apple Sign In endpoint | 2-3 hours |
| Google Sign In endpoint | 2-3 hours |
| Facebook Login endpoint | 2-3 hours |
| Testing & debugging | 2-4 hours |
| **Total** | **1-2 days** |

---

**Document Created**: December 30, 2025
**For**: Backend Team
**iOS App Status**: Ready to integrate once endpoints are available
