package main

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	_ "github.com/lib/pq"
)

var knownTokens = map[string]string{
	"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": "USDC",
	"0xdac17f958d2ee523a2206206994597c13d831ec7": "USDT",
	"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": "WETH",
	"0x6b175474e89094c44da98b954eedeac495271d0f": "DAI",
	"0x2260fac5e5542a773aa44fbcfedf7c193bc2c599": "WBTC",
}

var transferTopic = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	rpcURL := os.Getenv("ETH_RPC_URL")

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println("DB error:", err)
		os.Exit(1)
	}
	defer db.Close()

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		fmt.Println("RPC error:", err)
		os.Exit(1)
	}
	defer client.Close()

	ctx := context.Background()
	startBlock := int64(25592100)
	endBlock := int64(25999999)

	fmt.Printf("\n  Simple Indexer — blocks %d to %d\n\n", startBlock, endBlock)

	totalInserted := 0

	for blockNum := startBlock; blockNum <= endBlock; blockNum++ {
		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(blockNum),
			ToBlock:   big.NewInt(blockNum),
			Topics:    [][]common.Hash{{transferTopic}},
		}

		logs, err := client.FilterLogs(ctx, query)
		if err != nil {
			fmt.Printf("  Block %d error: %v\n", blockNum, err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		for _, log := range logs {
			if len(log.Topics) < 3 || len(log.Data) < 32 {
				continue
			}

			tokenAddr := strings.ToLower(log.Address.Hex())
			symbol, ok := knownTokens[tokenAddr]
			if !ok {
				continue
			}

			from := strings.ToLower(common.HexToAddress(log.Topics[1].Hex()).Hex())
			to := strings.ToLower(common.HexToAddress(log.Topics[2].Hex()).Hex())
			value := new(big.Int).SetBytes(log.Data[:32])
			txHash := log.TxHash.Hex()

			_, err := db.Exec(`
				INSERT INTO token_transfers
					(tx_hash, block_number, log_index, token_address, from_address, to_address, value)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (tx_hash, log_index) DO NOTHING`,
				txHash, blockNum, log.Index, tokenAddr, from, to, value.String())

			if err != nil {
				fmt.Printf("  INSERT ERROR: %v\n", err)
				continue
			}

			totalInserted++
			fmt.Printf("  💸 %-6s %s -> %s\n", symbol, from[:10], to[:10])
		}

		if blockNum%10 == 0 {
			fmt.Printf("  Block %d done — %d transfers saved\n", blockNum, totalInserted)
		}
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Printf("\n  Done! Total inserted: %d\n", totalInserted)
}
