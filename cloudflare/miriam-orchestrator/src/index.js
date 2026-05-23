const DEFAULT_BATCH_LIMIT = 500;
const EVALUATE_PATH = "/internal/miriam/evaluate";

export default {
  async scheduled(_event, env, ctx) {
    ctx.waitUntil(runMiriamEvaluation(env, "cron"));
  },

  async fetch(request, env) {
    const url = new URL(request.url);
    if (request.method === "GET" && url.pathname === "/health") {
      return json({ status: "ok", worker: "rail-miriam-orchestrator" });
    }

    if (request.method === "POST" && url.pathname === "/evaluate") {
      const result = await runMiriamEvaluation(env, "manual");
      return json(result, result.ok ? 200 : 502);
    }

    return json({ error: "not_found" }, 404);
  },
};

async function runMiriamEvaluation(env, source) {
  const baseURL = requiredEnv(env, "RAIL_API_BASE_URL").replace(/\/+$/, "");
  const apiKey = requiredEnv(env, "RAIL_INTERNAL_API_KEY");
  const batchLimit = parseBatchLimit(env.BATCH_LIMIT);

  const response = await postWithRetry(`${baseURL}${EVALUATE_PATH}`, apiKey, {
    event_type: "worker_sweep",
    limit: batchLimit,
    source,
  });

  const payload = await response.json().catch(() => ({}));
  return {
    ok: response.ok,
    status: response.status,
    source,
    result: payload,
  };
}

async function postWithRetry(url, apiKey, body) {
  const response = await postJSON(url, apiKey, body);
  if (response.status !== 429 && response.status < 500) {
    return response;
  }

  await sleep(750);
  return postJSON(url, apiKey, body);
}

function postJSON(url, apiKey, body) {
  return fetch(url, {
    method: "POST",
    headers: {
      "Authorization": `Bearer ${apiKey}`,
      "Content-Type": "application/json",
      "User-Agent": "rail-miriam-orchestrator/1.0",
    },
    body: JSON.stringify(body),
  });
}

function parseBatchLimit(raw) {
  const parsed = Number.parseInt(raw || "", 10);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return DEFAULT_BATCH_LIMIT;
  }
  return Math.min(parsed, 5000);
}

function requiredEnv(env, name) {
  const value = env[name];
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function json(payload, status = 200) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: {
      "Content-Type": "application/json",
      "Cache-Control": "no-store",
    },
  });
}
