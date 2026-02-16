# TestFlight Backend Checklist

## Configuration Issues to Check

### 1. CORS Configuration ⚠️
The backend has CORS middleware configured (`internal/api/middleware/middleware.go` line 160-190).

**Check your `.env` or config file for:**
```env
# Backend .env or config.yaml
server:
  allowed_origins:
    - "https://yourfrontendurl.com"  # Your actual frontend domain/app
    - "http://localhost:*"  # For dev testing (if needed)
```

**Issues to verify:**
- [ ] `allowed_origins` is configured (not empty)
- [ ] The frontend domain is in the allowed list
- [ ] For TestFlight: the actual TestFlight app URL or domain
- [ ] CORS headers are being returned properly

**Test CORS:**
```bash
# From any machine, test the OPTIONS request
curl -i -X OPTIONS https://your-api-domain.com/api/v1/auth/register \
  -H "Origin: https://yourfrontendurl.com" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: Content-Type"

# Should return:
# Access-Control-Allow-Origin: https://yourfrontendurl.com
# Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS
```

---

### 2. Register Endpoint Validation ✅ FIXED
**Status**: Enhanced in this round of fixes

**What was fixed:**
- Unverified users now get proper pending registration entry created
- Error handling improved for missing identifiers

**Verify:**
- [ ] Existing unverified user can re-attempt signup
- [ ] Backend logs show "Pending registration created for unverified user"

---

### 3. API Response Format Validation

The backend responds with standard format:
```json
{
  "code": "...",
  "message": "...",
  "details": {...}
}
```

**Check:**
- [ ] Register endpoint returns 202 ACCEPTED with proper response body
- [ ] Error responses include `code` and `message` fields
- [ ] No unexpected status codes (4xx vs 5xx)

**Test:**
```bash
# New user signup
curl -X POST https://your-api-domain.com/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com"}'

# Should return 202 with response body
```

---

### 4. Email Verification Service

The Register handler calls `verificationService.GenerateAndSendCode()`.

**Verify:**
- [ ] Email/SMS service is properly configured
- [ ] Verification codes are being sent (check email service logs)
- [ ] Timeout: not too long (causing client timeout)

**Check in backend logs:**
```
Failed to send verification code
Verification code sent to...
Too many verification code send attempts
```

---

### 5. Redis Configuration

Pending registrations are stored in Redis with 10-minute TTL.

**Verify:**
```bash
# Backend .env
redis:
  host: "localhost"  # Or your Redis server
  port: 6379
  password: ""  # If needed
  db: 0
```

**Test Redis connectivity:**
```bash
# From backend server
redis-cli -h your-redis-host ping
# Should return: PONG
```

**Check pending registrations:**
```bash
# In Redis CLI
redis-cli
KEYS "pending_registration:*"
GET "pending_registration:email:user@example.com"
```

---

### 6. Database Configuration

Users are persisted to the database.

**Verify:**
```bash
# Backend .env
database:
  host: "localhost"
  port: 5432
  name: "rail_db"
  user: "rails_user"
  password: "***"
```

**Test:**
```bash
# From backend server
psql -h your-db-host -U rails_user -d rail_db -c "SELECT COUNT(*) FROM users;"
```

---

## Network Error Root Cause Analysis

If users are getting network errors on signup, go through this systematically:

### Step 1: Check Frontend API URL
```bash
# Frontend .env
EXPO_PUBLIC_API_URL="https://your-api-domain.com/api/v1"
```

**Common issues:**
- ❌ `http://localhost:3000` (wrong protocol or port for production)
- ❌ Missing `/api/v1` prefix
- ❌ Pointing to old server
- ✅ `https://api.yourdomain.com/api/v1` (correct)

### Step 2: Check Backend is Running
```bash
# From any client, test health endpoint
curl -i https://your-api-domain.com/api/v1/health

# Should return 200 OK
```

If this fails:
- Backend not running
- Wrong domain/port
- Firewall blocking
- Load balancer misconfigured

### Step 3: Check DNS Resolution
```bash
# From client
nslookup your-api-domain.com
dig your-api-domain.com

# Should return your server IP
```

### Step 4: Check SSL Certificate
```bash
# Verify certificate is valid
openssl s_client -connect your-api-domain.com:443 -showcerts

# Check:
# - Expiration date
# - Common Name (CN) matches your domain
# - No self-signed errors
```

### Step 5: Check CORS Headers
```bash
# From frontend domain, test preflight
curl -i -X OPTIONS https://your-api-domain.com/api/v1/auth/register \
  -H "Origin: https://your-frontend-domain.com" \
  -H "Access-Control-Request-Method: POST"
```

**Must return:**
```
HTTP/1.1 200 OK
Access-Control-Allow-Origin: https://your-frontend-domain.com
Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS
```

If missing `Access-Control-Allow-Origin`:
- CORS not configured in backend
- Frontend domain not in allowed list
- Check `server.allowed_origins` in config

### Step 6: Check Request is Reaching Backend
Enable request logging and check:
```bash
# Backend logs should show
POST /api/v1/auth/register 202 ACCEPTED
```

If not appearing:
- Frontend not actually sending request
- Reverse proxy intercepting
- API gateway issue

### Step 7: Inspect Network Error Details
From frontend logs, look for:
```
[API] Network error - no response from server
code: ENOTFOUND        # DNS issue
code: ECONNREFUSED     # Connection refused
code: ECONNABORTED     # Timeout
code: ERR_TLS_CERT     # SSL issue
```

---

## Testing Checklist

### Presalive Testing
- [ ] Backend is deployed and healthy
- [ ] Database is connected
- [ ] Redis is connected
- [ ] Email service is configured
- [ ] API URL in frontend matches backend
- [ ] CORS allows frontend domain
- [ ] SSL certificate is valid

### Register Endpoint Testing
```bash
# New user signup
curl -X POST https://your-api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"newuser@test.com"}'

# Should return 202 ACCEPTED

# Check email was sent (check email service logs)
# Check pending registration in Redis
redis-cli GET "pending_registration:email:newuser@test.com"
```

### Verify Endpoint Testing
```bash
# After getting verification code from email
curl -X POST https://your-api/v1/auth/verify \
  -H "Content-Type: application/json" \
  -d '{"email":"newuser@test.com","code":"123456"}'

# Should return 200 OK with tokens
```

### Existing Unverified User
```bash
# Create user with valid email
curl -X POST https://your-api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"existing@test.com"}'

# Mark email verified in DB (simulate verification)
psql -c "UPDATE users SET email_verified=true WHERE email='existing@test.com';"

# Try to signup again with same email
curl -X POST https://your-api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"existing@test.com'}"

# Should return 202 ACCEPTED (not 409 CONFLICT)
```

---

## Environment Variables Needed

### Minimum for TestFlight
```env
# Server
SERVER_PORT=3000
SERVER_HOST=0.0.0.0
SERVER_ALLOWED_ORIGINS=["https://your-frontend.com"]

# Database
DATABASE_HOST=your-db-host
DATABASE_PORT=5432
DATABASE_NAME=rail_db
DATABASE_USER=rails_user
DATABASE_PASSWORD=***

# Redis
REDIS_HOST=your-redis-host
REDIS_PORT=6379

# JWT
JWT_SECRET=your-secret-key-min-32-chars
JWT_ACCESS_TTL=7200
JWT_REFRESH_TTL=604800

# Email
EMAIL_SERVICE=sendgrid  # or your service
EMAIL_API_KEY=***

# Verification
VERIFICATION_EXPIRY=600  # 10 minutes
VERIFICATION_SMS_SERVICE=twillio  # if using SMS
```

---

## Performance Considerations

### Register Endpoint Performance
Should complete in < 2 seconds:
1. Validate input (10ms)
2. Check if user exists (50ms)
3. Generate and send verification code (1500ms - mostly email service)
4. Store pending registration (50ms)

**If taking > 3 seconds:** check email service or database

---

## Security Considerations

✅ Already implemented:
- Rate limiting on login (5 attempts per 5 minutes)
- Rate limiting on forgot-password (3 attempts per hour)
- Pending registration auto-expires (10 minutes)
- Email verification required before full account creation
- No password hash stored until onboarding

---

## Monitoring to Set Up

Add monitoring/alerts for:
1. Register endpoint error rate (target: < 1%)
2. Register endpoint latency (target: < 2s avg, < 5s p95)
3. Email service failures
4. Redis connectivity issues
5. Database connectivity issues
6. CORS rejection count

---

## Files Modified in This Round

✅ `internal/api/handlers/auth/auth_handlers.go`
- Enhanced unverified user registration flow
- Added proper pending registration creation

---

## Next Steps

1. **Deploy** backend with fixes
2. **Verify Configuration** matches checklist above
3. **Test Register Flow** with curl commands above
4. **Monitor Logs** for errors during TestFlight
5. **Collect User Feedback** on network errors (if any)

---

## Quick Debug: Network Error on Signup

If users report "Network error" during signup:

1. Check frontend logs for error code (ENOTFOUND, ECONNREFUSED, etc.)
2. Check backend is running: `curl https://api-url/api/v1/health`
3. Check DNS: `nslookup your-api-domain.com`
4. Check SSL: `openssl s_client -connect your-api-domain.com:443`
5. Check CORS: `curl -i -X OPTIONS https://api-url/v1/auth/register`
6. Check allowed_origins in backend config
7. Check request reaches backend logs
