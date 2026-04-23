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

let svc: Awaited<ReturnType<typeof createUmbraService>> | null = null;

// Auth middleware — reject requests without valid token
app.use((req, res, next) => {
  if (req.path === "/health") return next(); // health check is public
  if (!AUTH_TOKEN) return next(); // no token configured = dev mode
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

app.post("/init", async (req, res) => {
  try {
    const { privateKey } = req.body;
    if (!privateKey) { res.status(400).json({ error: "privateKey required" }); return; }
    svc = await createUmbraService({
      privateKey, rpcUrl: RPC_URL, rpcWsUrl: RPC_WS_URL,
      network: NETWORK, indexerUrl: INDEXER_URL, relayerUrl: RELAYER_URL,
    });
    res.json({ status: "initialized", address: svc.address });
  } catch (err: any) { res.status(500).json({ error: err.message }); }
});

app.post("/register", async (_req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try { res.json(await svc.register()); }
  catch (err: any) { res.status(500).json({ error: err.message }); }
});

app.post("/shield", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try {
    const { mint, amount, destination } = req.body;
    res.json(await svc.shield(mint, BigInt(amount), destination));
  } catch (err: any) { res.status(500).json({ error: err.message }); }
});

app.post("/unshield", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try {
    const { mint, amount, destination } = req.body;
    res.json(await svc.unshield(mint, BigInt(amount), destination));
  } catch (err: any) { res.status(500).json({ error: err.message }); }
});

app.post("/balance", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try { res.json(await svc.getEncryptedBalance(req.body.mint)); }
  catch (err: any) { res.status(500).json({ error: err.message }); }
});

app.post("/scan-utxos", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try { res.json(await svc.scanUtxos(req.body.treeIndex ?? 0, req.body.startIndex ?? 0)); }
  catch (err: any) { res.status(500).json({ error: err.message }); }
});

app.post("/viewing-key", async (req, res) => {
  if (!svc) { res.status(503).json({ error: "not initialized" }); return; }
  try {
    const { scope, year, month, day } = req.body;
    res.json(await svc.deriveViewingKey(scope, year, month, day));
  } catch (err: any) { res.status(500).json({ error: err.message }); }
});

app.listen(PORT, () => console.log(`Umbra sidecar listening on port ${PORT}`));
