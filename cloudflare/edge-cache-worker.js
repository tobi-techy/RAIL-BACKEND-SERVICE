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
const PRIVATE_CACHE_CONTROL = "private, no-store";

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

async function handleRequest(event) {
  const request = event.request;
  const url = new URL(request.url);
  const pathname = url.pathname;

  if (pathname === "/") {
    return fetch(request);
  }

  const cache = event.caches.default;
  const cacheKey = new Request(url.toString(), {
    method: request.method,
    headers: request.headers,
  });

  if (isCacheableMethod(request.method)) {
    const ttl = getCacheTTL(pathname);
    
    if (ttl !== null && !isAuthRequired(pathname)) {
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

    if (isAuthRequired(pathname)) {
      const authHeader = request.headers.get("Authorization");
      if (!authHeader || !authHeader.startsWith("Bearer ")) {
        return new Response(JSON.stringify({ error: "UNAUTHORIZED", message: "Missing or invalid Authorization header" }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        });
      }

      const response = await fetch(request);
      const ttl = getCacheTTL(pathname);
      
      if (response.ok && ttl !== null) {
        const responseClone = response.clone();
        const headers = new Headers(responseClone.headers);
        headers.set("Cache-Control", `${PUBLIC_CACHE_CONTROL}${ttl}`);

        const cacheResponse = new Response(responseClone.body, {
          status: responseClone.status,
          statusText: responseClone.statusText,
          headers: headers,
        });

        event.waitUntil(cache.put(cacheKey, cacheResponse));
      }
      
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

  const ttl = 300;
  const nonceKey = `webhook:${timestamp}`;
  
  const cache = event.caches.default;
  const nonceCacheKey = new Request(nonceKey);
  const existing = await cache.match(nonceCacheKey);
  
  if (existing) {
    return new Response(JSON.stringify({ error: "REPLAY_DETECTED" }), {
      status: 409,
      headers: { "Content-Type": "application/json" },
    });
  }

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
