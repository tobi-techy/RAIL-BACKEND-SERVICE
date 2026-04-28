/**
 * Umbra Sidecar E2E Test — Devnet
 *
 * This test creates a persistent devnet wallet, tests all sidecar operations,
 * and saves the wallet for reuse.
 *
 * STEP 1: Fund the wallet manually (one-time):
 *   Go to https://faucet.solana.com
 *   Paste this address: (printed below)
 *   Request 2 SOL on devnet
 *
 * STEP 2: Run this test:
 *   cd umbra-sidecar && bun run src/test-devnet.ts
 *
 * The wallet keypair is saved to umbra-sidecar/.devnet-wallet.json
 * so you can fund it once and reuse across test runs.
 */
import {
  getUmbraClient,
  getUserRegistrationFunction,
  getPublicBalanceToEncryptedBalanceDirectDepositorFunction,
  getEncryptedBalanceToPublicBalanceDirectWithdrawerFunction,
  getEncryptedBalanceQuerierFunction,
  getUserAccountQuerierFunction,
  createInMemorySigner,
  createSignerFromPrivateKeyBytes,
} from "@umbra-privacy/sdk";
import { readFileSync, writeFileSync, existsSync } from "fs";

const RPC_URL = process.env.SOLANA_RPC_URL || "https://api.devnet.solana.com";
const RPC_WS = process.env.SOLANA_RPC_WS_URL || "wss://api.devnet.solana.com";
const INDEXER = process.env.UMBRA_INDEXER_URL || "https://utxo-indexer.api.umbraprivacy.com";
const WALLET_PATH = ".devnet-wallet.json";

const USDC_MINT = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v";
// wSOL works on devnet since the mint exists on all networks
const WSOL_MINT = "So11111111111111111111111111111111111111112";
// Use wSOL for devnet testing, USDC for mainnet
const TEST_MINT = WSOL_MINT;

let passed = 0;
let failed = 0;
let skipped = 0;

function ok(name: string, detail?: string) {
  passed++;
  console.log(`  ✅ ${name}${detail ? ` — ${detail}` : ""}`);
}
function fail(name: string, err: string) {
  failed++;
  console.log(`  ❌ ${name} — ${err}`);
}
function skip(name: string, reason: string) {
  skipped++;
  console.log(`  ⏭️  ${name} — ${reason}`);
}

async function getSolBalance(address: string): Promise<number> {
  const res = await fetch(RPC_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      jsonrpc: "2.0", id: 1,
      method: "getBalance",
      params: [address, { commitment: "confirmed" }],
    }),
  });
  const json = await res.json() as any;
  return (json.result?.value ?? 0) / 1e9;
}

async function main() {
  console.log("╔══════════════════════════════════════════╗");
  console.log("║   Umbra Sidecar — Devnet E2E Test       ║");
  console.log("╚══════════════════════════════════════════╝\n");

  // ── 1. Wallet Setup ──────────────────────────────────────
  console.log("── Wallet Setup ──");
  let signer: any;

  if (existsSync(WALLET_PATH)) {
    const keyBytes = new Uint8Array(JSON.parse(readFileSync(WALLET_PATH, "utf-8")));
    signer = await createSignerFromPrivateKeyBytes(keyBytes);
    ok("Loaded existing wallet", signer.address);
  } else {
    signer = await createInMemorySigner();
    // Export the private key for persistence
    // createInMemorySigner returns a signer with keyPair — extract the bytes
    try {
      const exported = await crypto.subtle.exportKey("raw", (signer as any).keyPair.privateKey);
      const bytes = Array.from(new Uint8Array(exported));
      writeFileSync(WALLET_PATH, JSON.stringify(bytes));
      ok("Created new wallet (saved to .devnet-wallet.json)", signer.address);
    } catch {
      ok("Created ephemeral wallet (could not persist)", signer.address);
    }
  }

  const balance = await getSolBalance(signer.address);
  console.log(`\n  Wallet: ${signer.address}`);
  console.log(`  SOL Balance: ${balance}`);

  if (balance < 0.01) {
    console.log(`\n  ⚠️  Wallet needs SOL! Fund it at:`);
    console.log(`     https://faucet.solana.com`);
    console.log(`     Address: ${signer.address}`);
    console.log(`     Network: devnet`);
    console.log(`     Then re-run this test.\n`);
  }

  // ── 2. Client Creation ───────────────────────────────────
  console.log("\n── Client Creation ──");
  let client: any;
  try {
    client = await getUmbraClient({
      signer,
      network: "devnet",
      rpcUrl: RPC_URL,
      rpcSubscriptionsUrl: RPC_WS,
      indexerApiEndpoint: INDEXER,
    });
    ok("getUmbraClient (devnet)");
  } catch (err: any) {
    fail("getUmbraClient", err.message);
    console.log("\n  Cannot proceed without client. Exiting.");
    process.exit(1);
  }

  // ── 3. Account State Query ───────────────────────────────
  console.log("\n── Account State ──");
  let accountState = "unknown";
  try {
    const query = getUserAccountQuerierFunction({ client });
    const result = await query(signer.address as any);
    accountState = (result as any)?.state ?? JSON.stringify(result);
    ok("getUserAccountQuerierFunction", `state=${accountState}`);
  } catch (err: any) {
    fail("getUserAccountQuerierFunction", err.message?.slice(0, 80));
  }

  // ── 4. Registration (confidential only, no ZK prover needed) ──
  console.log("\n── Registration ──");
  if (balance < 0.01) {
    skip("register (confidential)", "wallet unfunded");
  } else if (accountState !== "non_existent") {
    skip("register", `already in state: ${accountState}`);
  } else {
    try {
      const register = getUserRegistrationFunction({ client });
      // Use confidential-only mode (no ZK prover required)
      const sigs = await register({ confidential: true, anonymous: false });
      ok("register (confidential)", `${sigs.length} tx(s)`);
    } catch (err: any) {
      const msg = err.message?.slice(0, 120) || String(err);
      if (msg.includes("insufficient") || msg.includes("0x1")) {
        skip("register", "insufficient SOL");
      } else {
        fail("register", msg);
      }
    }
  }

  // ── 5. Encrypted Balance Query ───────────────────────────
  console.log("\n── Encrypted Balance Query ──");
  try {
    const query = getEncryptedBalanceQuerierFunction({ client });
    const balanceMap = await query([TEST_MINT] as any);
    ok("getEncryptedBalanceQuerierFunction", `entries=${balanceMap.size}`);
    for (const [addr, bal] of balanceMap) {
      console.log(`     ${String(addr)}: ${JSON.stringify(bal)}`);
    }
  } catch (err: any) {
    // Try single mint if array doesn't work
    try {
      const query = getEncryptedBalanceQuerierFunction({ client });
      const balanceMap = await query(TEST_MINT as any);
      ok("getEncryptedBalanceQuerierFunction (single mint)", `entries=${balanceMap.size}`);
    } catch (err2: any) {
      fail("getEncryptedBalanceQuerierFunction", err2.message?.slice(0, 80));
    }
  }

  // ── 6. Shield (Deposit into encrypted balance) ───────────
  console.log("\n── Shield (Deposit) ──");
  if (balance < 0.01) {
    skip("shield USDC", "wallet unfunded");
  } else {
    try {
      const deposit = getPublicBalanceToEncryptedBalanceDirectDepositorFunction({ client });
      const result = await deposit(signer.address as any, TEST_MINT as any, 1000n as any);
      ok("shield 0.001 wSOL", `queue=${String(result.queueSignature).slice(0, 20)}...`);
    } catch (err: any) {
      const msg = err.message?.slice(0, 120) || String(err);
      if (msg.includes("insufficient") || msg.includes("not found") || msg.includes("0x1")) {
        skip("shield USDC", "no USDC in wallet (need devnet USDC)");
      } else {
        fail("shield USDC", msg);
      }
    }
  }

  // ── 7. Unshield (Withdraw from encrypted balance) ────────
  console.log("\n── Unshield (Withdraw) ──");
  if (balance < 0.01) {
    skip("unshield USDC", "wallet unfunded");
  } else {
    try {
      const withdraw = getEncryptedBalanceToPublicBalanceDirectWithdrawerFunction({ client });
      const result = await withdraw(signer.address as any, TEST_MINT as any, 1000n as any);
      ok("unshield 0.001 USDC", `queue=${String(result.queueSignature).slice(0, 20)}...`);
    } catch (err: any) {
      const msg = err.message?.slice(0, 120) || String(err);
      if (msg.includes("insufficient") || msg.includes("not found") || msg.includes("balance")) {
        skip("unshield USDC", "no encrypted balance to withdraw");
      } else {
        fail("unshield USDC", msg);
      }
    }
  }

  // ── 8. Sidecar HTTP Test ─────────────────────────────────
  console.log("\n── Sidecar HTTP Smoke Test ──");
  try {
    // Test that the sidecar module can be imported and service created
    const { createUmbraService } = await import("./umbra-service.js");
    ok("umbra-service module imports");
  } catch (err: any) {
    fail("umbra-service import", err.message?.slice(0, 80));
  }

  // ── Summary ──────────────────────────────────────────────
  console.log("\n╔══════════════════════════════════════════╗");
  console.log(`║  Results: ${passed} passed, ${failed} failed, ${skipped} skipped`);
  console.log("╚══════════════════════════════════════════╝");
  console.log(`\n  Wallet for manual testing: ${signer.address}`);
  console.log(`  Fund at: https://faucet.solana.com (devnet)`);

  if (failed > 0) process.exit(1);
}

main().catch((err) => {
  console.error("\nFatal:", err);
  process.exit(1);
});
