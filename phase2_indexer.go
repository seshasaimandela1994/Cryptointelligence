package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

var knownTokens = map[string]string{
	"0xdac17f958d2ee523a2206206994597c13d831ec7": "USDT",
	"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": "USDC",
	"0x6b175474e89094c44da98b954eedeac495271d0f": "DAI",
	"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": "WETH",
	"0x2260fac5e5542a773aa44fbcfedf7c193bc2c599": "WBTC",
}

func main() {
	_ = godotenv.Load(".env")

	dbURL  := os.Getenv("DATABASE_URL")
	rpcURL := os.Getenv("ETH_RPC_URL")
	startBlockStr := os.Getenv("START_BLOCK")

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("DB error: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		fmt.Printf("ETH error: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	startBlock := uint64(0)
	fmt.Sscanf(startBlockStr, "%d", &startBlock)
	if startBlock == 0 {
		startBlock = 24719493
	}

	latestNum, _ := client.BlockNumber(ctx)
	
	// Cap at 40,000 blocks for manageable indexing
	targetBlock := startBlock + 40000
	if targetBlock > latestNum {
		targetBlock = latestNum
	}
	latestNum = targetBlock

	fmt.Println()
	fmt.Println("  CryptoIntelligence - Phase 2 Indexer")
	fmt.Println("  ======================================")
	fmt.Printf("  Indexing blocks %d to %d\n", startBlock, latestNum)
	fmt.Printf("  Total blocks: %d\n\n", latestNum-startBlock)

	totalReceipts := 0
	totalLogs     := 0
	totalTransfers := 0
	startTime := time.Now()

	for blockNum := startBlock; blockNum <= latestNum; blockNum++ {
		header, err := client.HeaderByNumber(ctx, new(big.Int).SetUint64(blockNum))
		if err != nil {
			fmt.Printf("  Block %d error: %v\n", blockNum, err)
			continue
		}

		txCount, _ := client.TransactionCount(ctx, header.Hash())
		blockTime  := time.Unix(int64(header.Time), 0).UTC()

		// Store block
		_, _ = pool.Exec(ctx, `
			INSERT INTO blocks (number, hash, parent_hash, timestamp, miner,
			                    gas_used, gas_limit, tx_count)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (number) DO NOTHING`,
			blockNum,
			header.Hash().Hex(),
			header.ParentHash.Hex(),
			blockTime,
			strings.ToLower(header.Coinbase.Hex()),
			header.GasUsed,
			header.GasLimit,
			txCount,
		)

		// Fetch receipts for each transaction
		block, err := client.BlockByNumber(ctx, new(big.Int).SetUint64(blockNum))
		if err != nil {
			fmt.Printf("  Block %d fetch error: %v\n", blockNum, err)
			continue
		}

		blockReceipts := 0
		blockLogs     := 0
		blockTransfers := 0

		for _, tx := range block.Transactions() {
			receipt, err := client.TransactionReceipt(ctx, tx.Hash())
			if err != nil {
				continue
			}

			// Store receipt
			_, _ = pool.Exec(ctx, `
				INSERT INTO transaction_receipts
				    (tx_hash, block_number, block_timestamp, status,
				     gas_used, cumulative_gas_used, log_count)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (tx_hash) DO NOTHING`,
				tx.Hash().Hex(),
				blockNum,
				blockTime,
				receipt.Status,
				receipt.GasUsed,
				receipt.CumulativeGasUsed,
				len(receipt.Logs),
			)
			blockReceipts++

			// Store event logs + decode ERC-20 transfers
			for _, log := range receipt.Logs {
				topic0 := ""
				topic1 := ""
				topic2 := ""
				if len(log.Topics) > 0 { topic0 = log.Topics[0].Hex() }
				if len(log.Topics) > 1 { topic1 = log.Topics[1].Hex() }
				if len(log.Topics) > 2 { topic2 = log.Topics[2].Hex() }

				_, _ = pool.Exec(ctx, `
					INSERT INTO event_logs
					    (tx_hash, block_number, block_timestamp,
					     log_index, address, topic0, topic1, topic2)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
					ON CONFLICT DO NOTHING`,
					tx.Hash().Hex(),
					blockNum,
					blockTime,
					log.Index,
					strings.ToLower(log.Address.Hex()),
					topic0, topic1, topic2,
				)
				blockLogs++

				// Decode ERC-20 Transfer
				if len(log.Topics) == 3 && topic0 == transferTopic {
					from  := "0x" + topic1[26:]
					to    := "0x" + topic2[26:]
					value := new(big.Int)
					if len(log.Data) >= 32 {
						value.SetBytes(log.Data[:32])
					}
					tokenAddr := strings.ToLower(log.Address.Hex())
					symbol := knownTokens[tokenAddr]
					if symbol == "" { symbol = "ERC20" }

					_, _ = pool.Exec(ctx, `
						INSERT INTO token_transfers
						    (tx_hash, block_number, block_timestamp,
						     log_index, token_address, from_address,
						     to_address, value)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
						ON CONFLICT DO NOTHING`,
						tx.Hash().Hex(),
						blockNum,
						blockTime,
						log.Index,
						tokenAddr,
						from, to,
						value.String(),
					)
					blockTransfers++

					// Print known token transfers
					if _, ok := knownTokens[tokenAddr]; ok {
						shortFrom := from[:6] + "..." + from[len(from)-4:]
						shortTo   := to[:6]   + "..." + to[len(to)-4:]
						fmt.Printf("  💸 %-6s  %s -> %s\n",
							symbol, shortFrom, shortTo)
					}
				}

				// Detect contract deployment
				if receipt.ContractAddress != (common.Address{}) {
					addr := strings.ToLower(receipt.ContractAddress.Hex())
					var sender string
					if tx.To() == nil {
						sender = "unknown"
					}
					_, _ = pool.Exec(ctx, `
						INSERT INTO contracts
						    (address, deployer, deploy_tx_hash,
						     deploy_block, deploy_timestamp)
						VALUES ($1,$2,$3,$4,$5)
						ON CONFLICT (address) DO NOTHING`,
						addr, sender,
						tx.Hash().Hex(),
						blockNum, blockTime,
					)
				}
			}
		}

		totalReceipts  += blockReceipts
		totalLogs      += blockLogs
		totalTransfers += blockTransfers

		elapsed := time.Since(startTime).Seconds()
		blocksProcessed := blockNum - startBlock + 1
		bps := float64(blocksProcessed) / elapsed

		fmt.Printf("  Block #%d | receipts:%d logs:%d transfers:%d | %.1f blocks/sec\n",
			blockNum, blockReceipts, blockLogs, blockTransfers, bps)

		// Update indexer state
		_, _ = pool.Exec(ctx,
			`UPDATE indexer_state SET value=$1, updated_at=NOW() WHERE key=$2`,
			fmt.Sprintf("%d", blockNum), "last_indexed_block",
		)
	}

	fmt.Println()
	fmt.Printf("  Done! Receipts:%d Logs:%d Transfers:%d\n",
		totalReceipts, totalLogs, totalTransfers)
}
