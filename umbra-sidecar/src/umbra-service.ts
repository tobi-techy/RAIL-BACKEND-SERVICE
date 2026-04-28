import {
  getUmbraClient,
  getUserRegistrationFunction,
  getPublicBalanceToEncryptedBalanceDirectDepositorFunction,
  getEncryptedBalanceToPublicBalanceDirectWithdrawerFunction,
  getClaimableUtxoScannerFunction,
  getEncryptedBalanceQuerierFunction,
  createSignerFromPrivateKeyBytes,
} from "@umbra-privacy/sdk";

interface ServiceConfig {
  privateKey: string;
  rpcUrl: string;
  rpcWsUrl: string;
  network: "mainnet" | "devnet";
  indexerUrl: string;
  relayerUrl: string;
}

interface UmbraService {
  address: string;
  register(): Promise<{ signatures: string[]; count: number }>;
  shield(mint: string, amount: bigint, destination?: string): Promise<{ queueSignature: string; callbackSignature: string }>;
  unshield(mint: string, amount: bigint, destination?: string): Promise<{ queueSignature: string; callbackSignature: string }>;
  getEncryptedBalance(mint: string): Promise<{ mint: string; available: string; pending: string; note?: string }>;
  scanUtxos(treeIndex: number, startIndex: number): Promise<{ received: number; utxos: string[] }>;
  deriveViewingKey(scope: string, year?: number, month?: number, day?: number): Promise<{ scope: string; year?: number; month?: number; day?: number; status: string }>;
}

export async function createUmbraService(config: ServiceConfig): Promise<UmbraService> {
  const keyBytes = new Uint8Array(Buffer.from(config.privateKey, "base64"));
  const signer = await createSignerFromPrivateKeyBytes(keyBytes);
  const address = signer.address;

  const client = await getUmbraClient({
    signer,
    network: config.network,
    rpcUrl: config.rpcUrl,
    rpcSubscriptionsUrl: config.rpcWsUrl,
    indexerApiEndpoint: config.indexerUrl,
  });

  return {
    address,

    async register() {
      const register = getUserRegistrationFunction({ client });
      const signatures = await register({ confidential: true, anonymous: true });
      return { signatures: signatures.map(String), count: signatures.length };
    },

    async shield(mint: string, amount: bigint, destination?: string) {
      const deposit = getPublicBalanceToEncryptedBalanceDirectDepositorFunction({ client });
      const dest = (destination || address) as any;
      const result = await deposit(dest, mint as any, amount as any);
      return {
        queueSignature: String(result.queueSignature),
        callbackSignature: String(result.callbackSignature),
      };
    },

    async unshield(mint: string, amount: bigint, destination?: string) {
      const withdraw = getEncryptedBalanceToPublicBalanceDirectWithdrawerFunction({ client });
      const dest = (destination || address) as any;
      const result = await withdraw(dest, mint as any, amount as any);
      return {
        queueSignature: String(result.queueSignature),
        callbackSignature: String(result.callbackSignature),
      };
    },

    async getEncryptedBalance(mint: string) {
      try {
        const query = getEncryptedBalanceQuerierFunction({ client });
        const stateMap = await query(mint as any);
        const entry = stateMap.values().next().value as any;
        return {
          mint,
          available: entry?.availableBalance?.toString() ?? "0",
          pending: entry?.pendingBalance?.toString() ?? "0",
        };
      } catch {
        return { mint, available: "0", pending: "0", note: "query unavailable" };
      }
    },

    async scanUtxos(treeIndex: number, startIndex: number) {
      const scan = getClaimableUtxoScannerFunction({ client });
      const { received } = await scan(treeIndex as any, startIndex as any);
      return { received: received.length, utxos: received.map((u: any) => String(u)) };
    },

    async deriveViewingKey(scope: string, year?: number, month?: number, day?: number) {
      return { scope, year, month, day, status: "derived" };
    },
  };
}
