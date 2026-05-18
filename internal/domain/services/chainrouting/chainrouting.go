package chainrouting

import (
	"strings"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

func WalletChainFromCircleChain(chain string) entities.WalletChain {
	switch strings.ToUpper(chain) {
	case "ETH", "ETH-SEPOLIA":
		return entities.WalletChainEthereum
	case "BASE", "BASE-SEPOLIA":
		return entities.WalletChainBase
	case "MATIC", "MATIC-AMOY":
		return entities.WalletChainPolygon
	case "ARB", "ARB-SEPOLIA":
		return entities.WalletChainArbitrum
	case "OP", "OP-SEPOLIA":
		return entities.WalletChainOptimism
	case "AVAX", "AVAX-FUJI":
		return entities.WalletChainAvalanche
	default:
		return ""
	}
}

func CircleChainToChainRailsChain(chain string) string {
	switch strings.ToUpper(chain) {
	case "ETH":
		return "ETHEREUM_MAINNET"
	case "ETH-SEPOLIA":
		return "ETHEREUM_TESTNET"
	case "BASE":
		return "BASE_MAINNET"
	case "BASE-SEPOLIA":
		return "BASE_TESTNET"
	case "MATIC":
		return "POLYGON_MAINNET"
	case "MATIC-AMOY":
		return "POLYGON_TESTNET"
	case "ARB":
		return "ARBITRUM_MAINNET"
	case "ARB-SEPOLIA":
		return "ARBITRUM_TESTNET"
	case "OP":
		return "OPTIMISM_MAINNET"
	case "OP-SEPOLIA":
		return "OPTIMISM_TESTNET"
	case "AVAX":
		return "AVALANCHE_MAINNET"
	case "AVAX-FUJI":
		return "AVALANCHE_TESTNET"
	default:
		return ""
	}
}

func USDCTokenForChainRailsChain(chain string) string {
	switch chain {
	case "SOLANA_MAINNET":
		return "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	case "SOLANA_TESTNET":
		return "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
	case "ETHEREUM_MAINNET":
		return "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	case "ETHEREUM_TESTNET":
		return "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"
	case "BASE_MAINNET":
		return "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	case "BASE_TESTNET":
		return "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
	case "POLYGON_MAINNET":
		return "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"
	case "POLYGON_TESTNET":
		return "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	case "ARBITRUM_MAINNET":
		return "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"
	case "ARBITRUM_TESTNET":
		return "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d"
	case "OPTIMISM_MAINNET":
		return "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85"
	case "AVALANCHE_MAINNET":
		return "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E"
	default:
		return ""
	}
}

func ChainFromChainRailsChain(cr string) entities.Chain {
	switch cr {
	case "SOLANA_MAINNET", "SOLANA_TESTNET":
		return entities.ChainSOL
	case "ETHEREUM_MAINNET", "ETHEREUM_TESTNET":
		return entities.ChainETH
	case "BASE_MAINNET", "BASE_TESTNET":
		return entities.ChainBase
	case "ARBITRUM_MAINNET", "ARBITRUM_TESTNET":
		return entities.ChainArbitrum
	case "POLYGON_MAINNET":
		return entities.ChainMATIC
	case "OPTIMISM_MAINNET", "OPTIMISM_TESTNET":
		return entities.ChainOptimism
	case "AVALANCHE_MAINNET", "AVALANCHE_TESTNET":
		return entities.ChainAvalanche
	case "STARKNET_MAINNET", "STARKNET_TESTNET":
		return entities.ChainStarknet
	case "BNB_MAINNET", "BSC_MAINNET":
		return entities.ChainBNB
	default:
		return ""
	}
}
