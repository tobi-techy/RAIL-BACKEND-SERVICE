# 0G Network API Reference

## Storage API

### Upload Data
```http
POST /api/v1/zerog/storage/upload
Authorization: Bearer {token}
Content-Type: multipart/form-data

file: <binary>
namespace: string (optional)
metadata: json (optional)
```

**Response**:
```json
{
  "storage_id": "0x1234...",
  "checksum": "sha256:abcd...",
  "size": 1024,
  "namespace": "ai-summaries/",
  "uploaded_at": "2024-01-15T10:30:00Z"
}
```

### Download Data
```http
GET /api/v1/zerog/storage/{storage_id}
Authorization: Bearer {token}
```

**Response**: Binary data with headers
```
Content-Type: application/octet-stream
X-Checksum: sha256:abcd...
X-Size: 1024
```

### List Storage
```http
GET /api/v1/zerog/storage?namespace={namespace}&limit=50&offset=0
Authorization: Bearer {token}
```

**Response**:
```json
{
  "items": [
    {
      "storage_id": "0x1234...",
      "namespace": "ai-summaries/",
      "size": 1024,
      "uploaded_at": "2024-01-15T10:30:00Z"
    }
  ],
  "total": 100,
  "limit": 50,
  "offset": 0
}
```

## Compute API

### Generate Inference
```http
POST /api/v1/zerog/compute/inference
Authorization: Bearer {token}
Content-Type: application/json

{
  "model": "gpt-oss-120b",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant"},
    {"role": "user", "content": "Analyze my portfolio"}
  ],
  "max_tokens": 1000,
  "temperature": 0.7
}
```

**Response**:
```json
{
  "id": "inf_123",
  "model": "gpt-oss-120b",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Based on your portfolio..."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 50,
    "completion_tokens": 200,
    "total_tokens": 250
  },
  "cost": 0.005
}
```

### List Available Models
```http
GET /api/v1/zerog/compute/models
Authorization: Bearer {token}
```

**Response**:
```json
{
  "models": [
    {
      "id": "gpt-oss-120b",
      "name": "GPT OSS 120B",
      "max_tokens": 4096,
      "input_price": 0.00001,
      "output_price": 0.00002
    }
  ]
}
```

## Quota API

### Get User Quota
```http
GET /api/v1/zerog/quota
Authorization: Bearer {token}
```

**Response**:
```json
{
  "user_id": "uuid",
  "tier": "premium",
  "storage": {
    "used": 5368709120,
    "limit": 107374182400,
    "usage_percent": 5
  },
  "compute": {
    "used": 50000,
    "limit": 10000000,
    "usage_percent": 0.5
  },
  "cost": {
    "current": 25.50,
    "limit": 1000.00,
    "usage_percent": 2.55
  },
  "reset_at": "2024-02-01T00:00:00Z"
}
```

### Estimate Cost
```http
POST /api/v1/zerog/quota/estimate
Authorization: Bearer {token}
Content-Type: application/json

{
  "storage_bytes": 1073741824,
  "compute_tokens": 100000
}
```

**Response**:
```json
{
  "storage_cost": 0.10,
  "compute_cost": 2.00,
  "total_cost": 2.10,
  "estimated_at": "2024-01-15T10:30:00Z"
}
```

## Namespace API

### Create Namespace
```http
POST /api/v1/zerog/namespaces
Authorization: Bearer {token}
Content-Type: application/json

{
  "name": "my-project",
  "metadata": {
    "description": "Project data"
  }
}
```

**Response**:
```json
{
  "id": "uuid",
  "name": "my-project",
  "owner_id": "uuid",
  "status": "active",
  "quota": {
    "max_storage": 10737418240,
    "used_storage": 0,
    "max_objects": 10000,
    "used_objects": 0
  },
  "created_at": "2024-01-15T10:30:00Z"
}
```

### Get Namespace
```http
GET /api/v1/zerog/namespaces/{name}
Authorization: Bearer {token}
```

### Delete Namespace
```http
DELETE /api/v1/zerog/namespaces/{name}
Authorization: Bearer {token}
```

## Health & Metrics

### Health Check
```http
GET /api/v1/zerog/health
```

**Response**:
```json
{
  "status": "healthy",
  "storage": {
    "status": "healthy",
    "latency_ms": 50
  },
  "compute": {
    "status": "healthy",
    "latency_ms": 100
  },
  "circuit_breaker": {
    "state": "closed",
    "failures": 0
  }
}
```

### Get Metrics
```http
GET /api/v1/zerog/metrics
Authorization: Bearer {token}
```

**Response**:
```json
{
  "storage": {
    "uploads": 1000,
    "downloads": 500,
    "total_bytes": 10737418240,
    "errors": 5
  },
  "compute": {
    "requests": 200,
    "tokens": 500000,
    "errors": 2
  },
  "costs": {
    "storage": 1.00,
    "compute": 10.00,
    "total": 11.00
  }
}
```

## Error Responses

### 400 Bad Request
```json
{
  "error": "INVALID_REQUEST",
  "message": "File size exceeds maximum 10MB",
  "details": {
    "max_size": 10485760,
    "provided_size": 20971520
  }
}
```

### 429 Too Many Requests
```json
{
  "error": "QUOTA_EXCEEDED",
  "message": "Storage quota exceeded",
  "quota": {
    "used": 10737418240,
    "limit": 10737418240
  },
  "retry_after": 2592000
}
```

### 503 Service Unavailable
```json
{
  "error": "CIRCUIT_BREAKER_OPEN",
  "message": "Service temporarily unavailable",
  "retry_after": 30
}
```
