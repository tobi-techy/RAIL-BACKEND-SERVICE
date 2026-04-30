import express from "express";
import { createUmbraService } from "./umbra-service.js";

const app = express();
app.use(express.json());

const PORT = parseInt(process.env.UMBRA_SIDECAR_PORT || "3100", 10);
const AUTH_TOKEN = process.env.UMBRA_SIDECAR_AUTH_TOKEN || "";
const RPC_URL = process.env.SOLANA_RPC_URL || "https://api.mainnet-beta.solana.com";
const RPC_WS_URL = process.env.SOLANA_RPC_WS_URL || "wss://api.mainnet-beta.solana.com";
const NETWORK = (process.env.UMBRA_NETWORK || "mainnet") as "mainnet" | "devnet";
const INDEXER_URL = process.env.UMBRA_INDEXER_URL || "https://utxo-indexer.api.umbraprivacy.com";
const RELAYER_URL = process.env.UMBRA_RELAYER_URL || "https://relayer.api.umbraprivacy.com";

// SECURITY: Load private key from environment variable — never accept over HTTP.
const PRIVATE_KEY = process.env.UMBRA_PRIVATE_KEY || "";

let svc: Awaited<ReturnType<typeof createUmbraService>> | null = null;

// SECURITY: Require auth token in all non-dev environments. Fail to start if missing.
if (!AUTH_TOKEN) {
  const env = process.env.NODE_ENV || "development";
  if (env !== "development" && env !== "test") {
    console.error("FATAL: UMBRA_SIDECAR_AUTH_TOKEN is required in non-development environments");
    process.exit(1);
  }
  console.warn("WARNING: Running without auth token — development mode only");
}

// Auth middleware — reject requests without valid token
app.use((req, res, next) => {
  if (req.path === "/health") return next();
  if (!AUTH_TOKEN) return next(); // dev mode only (enforced above)
  const token = req.headers["x-sidecar-token"] || req.headers["authorization"]?.replace("Bearer ", "");
  if (token !== AUTH_TOKEN) {
    res.status(401).json({ error: "unauthorized" });
    return;
  }
  next();
});

app.get("/health", (_req, res) => {
  res.json({ status: svc ? "ready" : "initializing" });
});

// /init endpoint REMOVED — private keys must never be transmitted over HTTP.
// The sidecar now initializes from the UMBRA_PRIVATE_KEY environment variable at startup.

app.post("/register", async (_req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try { res.json(await svc.register()); }
  catch (err: any) { res.status(500).json({ error: "internal error" }); }
});

app.post("/shield", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try {
    const { mint, amount, destination } = req.body;
    res.json(await svc.shield(mint, BigInt(amount), destination));
  } catch (err: any) { res.status(500).json({ error: "internal error" }); }
});

app.post("/unshield", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try {
    const { mint, amount, destination } = req.body;
    res.json(await svc.unshield(mint, BigInt(amount), destination));
  } catch (err: any) { res.status(500).json({ error: "internal error" }); }
});

app.post("/balance", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try { res.json(await svc.getEncryptedBalance(req.body.mint)); }
  catch (err: any) { res.status(500).json({ error: "internal error" }); }
});

app.post("/scan-utxos", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try { res.json(await svc.scanUtxos(req.body.treeIndex ?? 0, req.body.startIndex ?? 0)); }
  catch (err: any) { res.status(500).json({ error: "internal error" }); }
});

app.post("/viewing-key", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try {
    const { scope, year, month, day } = req.body;
    res.json(await svc.deriveViewingKey(scope, year, month, day));
  } catch (err: any) { res.status(500).json({ error: "internal error" }); }
});

// Auto-initialize from environment variable at startup
async function init() {
  if (!PRIVATE_KEY) {
    console.warn("UMBRA_PRIVATE_KEY not set — sidecar will start but remain uninitialized");
    return;
  }
  try {
    svc = await createUmbraService({
      privateKey: PRIVATE_KEY, rpcUrl: RPC_URL, rpcWsUrl: RPC_WS_URL,
      network: NETWORK, indexerUrl: INDEXER_URL, relayerUrl: RELAYER_URL,
    });
    console.log(`Umbra sidecar initialized, address: ${svc.address}`);
  } catch (err) {
    console.error("Failed to initialize Umbra service:", err);
    process.exit(1);
  }
}

init().then(() => {
  app.listen(PORT, () => console.log(`Umbra sidecar listening on port ${PORT}`));
});
