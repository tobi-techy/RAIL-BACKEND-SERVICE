const ORIGIN = "https://api.userail.money";

const CACHE_CONFIG = {
  "/api/v1/assets": 300,
  "/api/v1/limits": 300,
  "/api/v1/kyc/status": 30,
  "/api/v1/portfolio/overview": 10,
  "/api/v1/portfolio": 10,
  "/api/v1/balances": 30,
  "/api/v1/account/station": 30,
  "/api/v1/market": 30,
};

const PUBLIC_CACHE_CONTROL = "public, max-age=";

function getCacheTTL(pathname) {
  for (const [prefix, ttl] of Object.entries(CACHE_CONFIG)) {
    if (pathname.startsWith(prefix)) {
      return ttl;
    }
  }
  return null;
}

function isCacheableMethod(method) {
  return method === "GET" || method === "HEAD";
}

function isAuthRequired(pathname) {
  const publicPaths = [
    "/api/v1/assets",
    "/api/v1/market",
    "/ping",
    "/health",
    "/ready",
    "/live",
  ];
  return !publicPaths.some(p => pathname.startsWith(p));
}

function getBodyHash(request) {
  return request.text().then(text => {
    const encoder = new TextEncoder();
    const data = encoder.encode(text);
    let hash = 0;
    for (let i = 0; i < data.length; i++) {
      const char = data.charCodeAt(i);
      hash = ((hash << 5) - hash) + char;
      hash = hash & hash;
    }
    return hash.toString(16);
  });
}

async function handleRequest(event) {
  const request = event.request;
  const url = new URL(request.url);
  const pathname = url.pathname;

  if (pathname === "/") {
    return fetch(request);
  }

  // DEFENSE-IN-DEPTH: Check authentication FIRST before any caching
  if (isAuthRequired(pathname)) {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader || !authHeader.startsWith("Bearer ")) {
      return new Response(JSON.stringify({ error: "UNAUTHORIZED", message: "Missing or invalid Authorization header" }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      });
    }
  }

  const cache = event.caches.default;

  // Only cache public endpoints - build cache key WITHOUT Authorization header
  const ttl = getCacheTTL(pathname);
  if (isCacheableMethod(request.method) && ttl !== null && !isAuthRequired(pathname)) {
    // Build cache key without sensitive headers - use empty headers for public caching
    const cacheKey = new Request(url.toString(), {
      method: request.method,
      headers: {}, // Empty headers for public cache key
    });

    const cached = await cache.match(cacheKey);
    if (cached) {
      const etag = cached.headers.get("ETag");
      const ifNoneMatch = request.headers.get("If-None-Match");
      
      if (etag && ifNoneMatch === etag) {
        return new Response(null, {
          status: 304,
          headers: {
            "ETag": etag,
            "Cache-Control": `${PUBLIC_CACHE_CONTROL}${ttl}`,
          },
        });
      }

      const response = cached.clone();
      response.headers.set("CF-Cache-Status", "HIT");
      return response;
    }

    const response = await fetch(request);
    if (response.ok) {
      const responseClone = response.clone();
      const headers = new Headers(responseClone.headers);
      headers.set("Cache-Control", `${PUBLIC_CACHE_CONTROL}${ttl}`);
      headers.set("CF-Cache-Status", "MISS");

      const cacheResponse = new Response(responseClone.body, {
        status: responseClone.status,
        statusText: responseClone.statusText,
        headers: headers,
      });

      event.waitUntil(cache.put(cacheKey, cacheResponse));
      return response;
    }
  }

  return fetch(request);
}

async function handleWebhook(event) {
  const request = event.request;
  const signature = request.headers.get("X-Signature");
  const timestamp = request.headers.get("X-Timestamp");

  if (!signature || !timestamp) {
    return new Response(JSON.stringify({ error: "MISSING_SIGNATURE" }), {
      status: 401,
      headers: { "Content-Type": "application/json" },
    });
  }

  // Validate timestamp is not older than 5 minutes
  const now = Math.floor(Date.now() / 1000);
  const webhookAge = now - parseInt(timestamp, 10);
  if (isNaN(webhookAge) || webhookAge > 300) { // 5 minutes = 300 seconds
    return new Response(JSON.stringify({ error: "WEBHOOK_EXPIRED", message: "Webhook timestamp is too old" }), {
      status: 401,
      headers: { "Content-Type": "application/json" },
    });
  }

  // Use signature as unique nonce key - signature should be unique per webhook payload
  // Fall back to body hash if signature is not unique enough
  let nonceKey = `webhook:${signature}`;
  
  // 24 hours TTL for replay protection
  const ttl = 86400;
  
  const cache = event.caches.default;
  const nonceCacheKey = new Request(nonceKey);
  const existing = await cache.match(nonceCacheKey);
  
  if (existing) {
    return new Response(JSON.stringify({ error: "REPLAY_DETECTED" }), {
      status: 409,
      headers: { "Content-Type": "application/json" },
    });
  }

  // TODO: Add actual signature verification here
  // Currently only checks if signature exists but doesn't verify it
  // The signature should be verified against a secret before processing
  
  const response = await fetch(request);
  
  if (response.ok) {
    const nonceResponse = new Response("nonce", {
      status: 200,
      headers: new Headers({
        "Cache-Control": `public, max-age=${ttl}`,
      }),
    });
    event.waitUntil(cache.put(nonceCacheKey, nonceResponse));
  }

  return response;
}

addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  
  if (url.pathname.startsWith("/api/v1/webhooks/")) {
    event.respondWith(handleWebhook(event));
  } else {
    event.respondWith(handleRequest(event));
  }
});
