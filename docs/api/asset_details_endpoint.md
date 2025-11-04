# Asset Details Endpoint Documentation

## Overview

The **Asset Details** endpoint provides comprehensive information about a specific tradable asset (stock, ETF, or crypto), including:

- Core asset information (symbol, exchange, tradability)
- User's position data (if they hold the asset)
- Trading constraints (min order sizes, supported order types)
- Market context (market hours, next open/close)
- Recent news articles related to the asset

This endpoint is designed to power a detailed asset view UI screen where users can see all relevant information about an asset before making investment decisions.

---

## Endpoint

```
GET /api/v1/assets/{symbol}/details
```

### Authentication
**Required**: Yes (Bearer token via JWT)

### Base URL
- **Development**: `http://localhost:8080`
- **Staging**: `https://api-staging.stackapp.com`
- **Production**: `https://api.stackapp.com`

---

## Path Parameters

| Parameter | Type   | Required | Description                           | Example |
|-----------|--------|----------|---------------------------------------|---------|
| `symbol`  | string | Yes      | Asset symbol (case-insensitive)       | `AAPL`  |

---

## Query Parameters

| Parameter      | Type    | Required | Default | Description                                          |
|----------------|---------|----------|---------|------------------------------------------------------|
| `account_id`   | string  | No       | -       | Alpaca account ID to fetch position data             |
| `include_news` | boolean | No       | `true`  | Whether to include recent news articles in response  |

---

## Response Structure

### Success Response (200 OK)

```json
{
  "asset_info": {
    "id": "f801f835-bfe6-4a9d-a6b1-ccbb84bfd75f",
    "symbol": "AAPL",
    "name": "Apple Inc.",
    "class": "us_equity",
    "exchange": "NASDAQ",
    "status": "active",
    "tradable": true,
    "marginable": true,
    "shortable": true,
    "easy_to_borrow": true,
    "fractionable": true
  },
  "position": {
    "quantity": "10.5",
    "avg_entry_price": "175.50",
    "market_value": "1893.00",
    "cost_basis": "1842.75",
    "unrealized_pl": "50.25",
    "unrealized_pl_percent": "2.73",
    "current_price": "180.28",
    "side": "long",
    "qty_available": "10.5"
  },
  "trading_info": {
    "min_order_size": "1",
    "min_trade_increment": "0.01",
    "price_increment": "0.01",
    "supports_market_orders": true,
    "supports_limit_orders": true,
    "supports_stop_orders": true,
    "extended_hours_trading": false
  },
  "market_context": {
    "is_market_open": false,
    "next_market_open": "2024-01-22T09:30:00-05:00",
    "timezone": "America/New_York"
  },
  "recent_news": [
    {
      "id": 12345,
      "headline": "Apple Announces New Product Line",
      "summary": "Apple unveils innovative new products...",
      "source": "Reuters",
      "url": "https://reuters.com/article/123",
      "created_at": "2024-01-21T14:32:00Z"
    }
  ],
  "metadata": {
    "timestamp": "2024-01-21T18:45:32Z",
    "request_id": "req_abc123xyz",
    "cache_status": "miss"
  }
}
```

### Response Without Position (User doesn't hold asset)

If the user doesn't hold a position in the requested asset, the `position` field will be `null`:

```json
{
  "asset_info": { ... },
  "position": null,
  "trading_info": { ... },
  "market_context": { ... },
  "recent_news": [ ... ],
  "metadata": { ... }
}
```

---

## Field Descriptions

### `asset_info`

Core asset identification and trading flags.

| Field            | Type    | Description                                    |
|------------------|---------|------------------------------------------------|
| `id`             | string  | Alpaca asset UUID                              |
| `symbol`         | string  | Trading symbol (e.g., AAPL, TSLA)              |
| `name`           | string  | Full company/asset name                        |
| `class`          | string  | Asset class (e.g., us_equity, crypto)          |
| `exchange`       | string  | Primary exchange (NASDAQ, NYSE, etc.)          |
| `status`         | string  | Asset status (active, inactive)                |
| `tradable`       | boolean | Whether asset can be traded                    |
| `marginable`     | boolean | Whether asset can be traded on margin          |
| `shortable`      | boolean | Whether asset can be shorted                   |
| `easy_to_borrow` | boolean | Whether asset is easy to borrow for shorting   |
| `fractionable`   | boolean | Whether fractional shares are supported        |

### `position` (nullable)

User's current position in the asset. Only present if `account_id` is provided and user holds the asset.

| Field                    | Type   | Description                                      |
|--------------------------|--------|--------------------------------------------------|
| `quantity`               | string | Total shares held (supports fractional)          |
| `avg_entry_price`        | string | Average price paid per share                     |
| `market_value`           | string | Current market value (quantity × current price)  |
| `cost_basis`             | string | Total amount paid for position                   |
| `unrealized_pl`          | string | Unrealized profit/loss in USD                    |
| `unrealized_pl_percent`  | string | Unrealized P/L as percentage                     |
| `current_price`          | string | Latest market price per share                    |
| `side`                   | string | Position side (long or short)                    |
| `qty_available`          | string | Shares available for trading (not held in orders)|

### `trading_info`

Trading constraints and supported order types.

| Field                     | Type    | Description                                  |
|---------------------------|---------|----------------------------------------------|
| `min_order_size`          | string  | Minimum quantity per order (nullable)        |
| `min_trade_increment`     | string  | Minimum increment for fractional trades      |
| `price_increment`         | string  | Minimum price increment (tick size)          |
| `supports_market_orders`  | boolean | Whether market orders are supported          |
| `supports_limit_orders`   | boolean | Whether limit orders are supported           |
| `supports_stop_orders`    | boolean | Whether stop/stop-limit orders are supported |
| `extended_hours_trading`  | boolean | Whether pre/post-market trading is available |

### `market_context`

Real-time market status and hours.

| Field                | Type       | Description                                      |
|----------------------|------------|--------------------------------------------------|
| `is_market_open`     | boolean    | Whether US equity market is currently open       |
| `next_market_open`   | timestamp  | Next market open time (EST) - if market closed   |
| `next_market_close`  | timestamp  | Next market close time (EST) - if market open    |
| `timezone`           | string     | Market timezone (America/New_York)               |

### `recent_news` (array, optional)

Latest news articles related to the asset (max 5). Omitted if `include_news=false`.

| Field        | Type      | Description                        |
|--------------|-----------|------------------------------------|
| `id`         | integer   | News article ID                    |
| `headline`   | string    | Article headline                   |
| `summary`    | string    | Brief article summary              |
| `source`     | string    | News source (e.g., Reuters, WSJ)   |
| `url`        | string    | Full article URL                   |
| `created_at` | timestamp | Article publication timestamp      |

### `metadata`

Response metadata for debugging and caching.

| Field          | Type      | Description                                |
|----------------|-----------|--------------------------------------------|
| `timestamp`    | timestamp | Response generation timestamp              |
| `request_id`   | string    | Unique request identifier for tracing      |
| `cache_status` | string    | Cache hit/miss status (future enhancement) |

---

## Error Responses

### 400 Bad Request

Returned when the symbol parameter is missing or invalid.

```json
{
  "code": "INVALID_PARAMETER",
  "error": "Asset symbol is required",
  "details": null
}
```

### 401 Unauthorized

Returned when authentication token is missing or invalid.

```json
{
  "code": "UNAUTHORIZED",
  "error": "Authentication required",
  "details": "Invalid or expired token"
}
```

### 404 Not Found

Returned when the requested asset does not exist.

```json
{
  "code": "ASSET_NOT_FOUND",
  "error": "Asset not found",
  "details": "INVALID_SYMBOL"
}
```

### 500 Internal Server Error

Returned when an unexpected error occurs.

```json
{
  "code": "ASSET_FETCH_ERROR",
  "error": "Failed to retrieve asset details",
  "details": null
}
```

---

## Usage Examples

### Example 1: Get AAPL details without position data

```bash
curl -X GET "https://api.stackapp.com/api/v1/assets/AAPL/details" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

**Response**: Returns asset info, trading info, market context, and news, but `position` will be `null`.

---

### Example 2: Get TSLA details with position data

```bash
curl -X GET "https://api.stackapp.com/api/v1/assets/TSLA/details?account_id=YOUR_ACCOUNT_ID" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

**Response**: Includes user's position in TSLA if they hold it.

---

### Example 3: Get asset details without news

```bash
curl -X GET "https://api.stackapp.com/api/v1/assets/NVDA/details?include_news=false" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

**Response**: Excludes the `recent_news` array for faster response times.

---

### Example 4: Mobile app request (React Native)

```javascript
const fetchAssetDetails = async (symbol, accountId) => {
  const response = await fetch(
    `https://api.stackapp.com/api/v1/assets/${symbol}/details?account_id=${accountId}`,
    {
      method: 'GET',
      headers: {
        'Authorization': `Bearer ${userToken}`,
        'Content-Type': 'application/json',
      },
    }
  );

  if (!response.ok) {
    throw new Error(`Error: ${response.status}`);
  }

  const data = await response.json();
  return data;
};

// Usage
const assetDetails = await fetchAssetDetails('AAPL', 'your-account-id');
console.log(assetDetails.position?.unrealized_pl); // "50.25"
```

---

## Best Practices

### 1. **Caching**

- Cache asset details on the client for **1-5 minutes** since asset metadata rarely changes.
- Cache position data for **30-60 seconds** to reflect price movements without excessive API calls.
- Cache news for **10-15 minutes** as it updates less frequently.

**Example client-side caching strategy**:

```javascript
const cache = {};
const CACHE_TTL = 60000; // 1 minute

async function getCachedAssetDetails(symbol) {
  const now = Date.now();
  if (cache[symbol] && (now - cache[symbol].timestamp < CACHE_TTL)) {
    return cache[symbol].data;
  }

  const data = await fetchAssetDetails(symbol);
  cache[symbol] = { data, timestamp: now };
  return data;
}
```

### 2. **Error Handling**

Always handle potential errors gracefully:

```javascript
try {
  const details = await fetchAssetDetails('AAPL', accountId);
  // Render UI
} catch (error) {
  if (error.response?.status === 404) {
    // Show "Asset not found" message
  } else if (error.response?.status === 401) {
    // Redirect to login
  } else {
    // Show generic error
  }
}
```

### 3. **Performance Optimization**

- Use `include_news=false` if news is not needed on initial load; fetch it separately on user interaction.
- Only provide `account_id` if the user is viewing their own holdings; omit for generic asset browsing.
- Batch multiple asset detail requests if needed (future enhancement: batch endpoint).

### 4. **Position Data Handling**

- Always check if `position` is `null` before accessing its fields:

```javascript
const hasPosition = assetDetails.position !== null;
const profitLoss = hasPosition ? assetDetails.position.unrealized_pl : 'N/A';
```

### 5. **Market Hours Awareness**

- Use `market_context.is_market_open` to show real-time vs delayed price indicators.
- Display `next_market_open` or `next_market_close` to inform users when trading resumes/ends.

---

## UI Integration Guidelines

### Suggested UI Sections

1. **Asset Header**
   - Display `asset_info.symbol` (e.g., AAPL)
   - Display `asset_info.name` (e.g., Apple Inc.)
   - Show current price from `position.current_price` (if available) or fetch from separate price endpoint

2. **Position Summary** (if `position` is not null)
   - Quantity: `position.quantity`
   - Avg Entry: `position.avg_entry_price`
   - Current Value: `position.market_value`
   - P/L: `position.unrealized_pl` (color-coded: green if positive, red if negative)
   - P/L %: `position.unrealized_pl_percent`

3. **Trading Info**
   - Min Order: `trading_info.min_order_size`
   - Fractional: Show badge if `asset_info.fractionable` is true
   - Order Types: Show icons for market, limit, stop orders if supported

4. **Market Status**
   - Show "Market Open" or "Market Closed" badge based on `market_context.is_market_open`
   - Display countdown to next open/close

5. **Recent News** (scrollable list)
   - Headline: `recent_news[].headline`
   - Source: `recent_news[].source`
   - Time: `recent_news[].created_at` (formatted as "2 hours ago")
   - Tap to open: `recent_news[].url`

---

## Rate Limiting

- **Limit**: 100 requests per minute per user
- **Header**: `X-RateLimit-Remaining` will indicate remaining requests
- **429 Response**: Returned when rate limit is exceeded

```json
{
  "code": "RATE_LIMIT_EXCEEDED",
  "error": "Too many requests",
  "details": "Retry after 60 seconds"
}
```

---

## Changelog

| Version | Date       | Changes                                           |
|---------|------------|---------------------------------------------------|
| 1.0.0   | 2024-01-21 | Initial release with comprehensive asset details  |

---

## Support

For technical support or questions, contact:
- **Email**: dev-support@stackapp.com
- **Slack**: #api-support
- **Documentation**: https://docs.stackapp.com
