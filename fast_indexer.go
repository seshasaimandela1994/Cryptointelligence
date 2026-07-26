package main

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
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

func getCheckpoint(db *sql.DB, jobName string, defaultStart int64) int64 {
	var lastBlock int64
	err := db.QueryRow(`SELECT last_block FROM indexer_checkpoints WHERE job_name = $1`, jobName).Scan(&lastBlock)
	if err != nil {
		return defaultStart
	}
	return lastBlock + 1
}

func saveCheckpoint(db *sql.DB, jobName string, blockNum int64) {
	db.Exec(`
		INSERT INTO indexer_checkpoints (job_name, last_block, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (job_name) DO UPDATE SET last_block = $2, updated_at = NOW()`,
		jobName, blockNum)
}

func processLogs(db *sql.DB, logs []types.Log, counter *int64) {
	for _, log := range logs {
		if len(log.Topics) < 3 || len(log.Data) < 32 {
			continue
		}
		tokenAddr := strings.ToLower(log.Address.Hex())
		if _, ok := knownTokens[tokenAddr]; !ok {
			continue
		}
		from := strings.ToLower(common.HexToAddress(log.Topics[1].Hex()).Hex())
		to := strings.ToLower(common.HexToAddress(log.Topics[2].Hex()).Hex())
		value := new(big.Int).SetBytes(log.Data[:32])

		_, err := db.Exec(`
			INSERT INTO token_transfers
				(tx_hash, block_number, log_index, token_address, from_address, to_address, value)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (tx_hash, log_index) DO NOTHING`,
			log.TxHash.Hex(), log.BlockNumber, log.Index, tokenAddr, from, to, value.String())
		if err == nil {
			atomic.AddInt64(counter, 1)
		}
	}
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	rpcURL := os.Getenv("ETH_RPC_URL")

	jobName := "fast_indexer"
	defaultStart := int64(1920000)
	endBlock := int64(25592099)
	batchRange := int64(2000)
	workers := 5

	if len(os.Args) > 1 {
		jobName = os.Args[1]
	}
	if len(os.Args) > 2 {
		s, _ := strconv.ParseInt(os.Args[2], 10, 64)
		if s >= 0 {
			defaultStart = s
		}
	}
	if len(os.Args) > 3 {
		e, _ := strconv.ParseInt(os.Args[3], 10, 64)
		if e > 0 {
			endBlock = e
		}
	}
	if len(os.Args) > 4 {
		w, _ := strconv.Atoi(os.Args[4])
		if w > 0 {
			workers = w
		}
	}
	if len(os.Args) > 5 {
		br, _ := strconv.ParseInt(os.Args[5], 10, 64)
		if br > 0 {
			batchRange = br
		}
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println("DB error:", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(workers + 5)
	defer db.Close()

	db.Exec(`
		CREATE TABLE IF NOT EXISTS indexer_checkpoints (
			job_name VARCHAR(64) PRIMARY KEY,
			last_block BIGINT,
			updated_at TIMESTAMP DEFAULT NOW()
		)`)

	startBlock := getCheckpoint(db, jobName, defaultStart)

	fmt.Printf("\n  Fast Indexer [%s]\n", jobName)
	fmt.Printf("  Range: %d -> %d | Batch: %d blocks/call | Workers: %d\n\n", startBlock, endBlock, batchRange, workers)

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		fmt.Println("RPC error:", err)
		os.Exit(1)
	}
	defer client.Close()

	ctx := context.Background()

	type rangeJob struct{ from, to int64 }
	var jobs []rangeJob
	for b := startBlock; b <= endBlock; b += batchRange {
		to := b + batchRange - 1
		if to > endBlock {
			to = endBlock
		}
		jobs = append(jobs, rangeJob{b, to})
	}

	jobChan := make(chan rangeJob, len(jobs))
	for _, j := range jobs {
		jobChan <- j
	}
	close(jobChan)

	var totalInserted int64
	var maxDone int64 = startBlock - 1
	var maxDoneMu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := range jobChan {
				query := ethereum.FilterQuery{
					FromBlock: big.NewInt(j.from),
					ToBlock:   big.NewInt(j.to),
					Topics:    [][]common.Hash{{transferTopic}},
				}

				result, err := client.FilterLogs(ctx, query)
				if err != nil {
					for sb := j.from; sb <= j.to; sb += 200 {
						se := sb + 199
						if se > j.to {
							se = j.to
						}
						subQuery := ethereum.FilterQuery{
							FromBlock: big.NewInt(sb),
							ToBlock:   big.NewInt(se),
							Topics:    [][]common.Hash{{transferTopic}},
						}
						subResult, subErr := client.FilterLogs(ctx, subQuery)
						if subErr != nil {
							time.Sleep(1 * time.Second)
							continue
						}
						processLogs(db, subResult, &totalInserted)
					}
				} else {
					processLogs(db, result, &totalInserted)
				}

				maxDoneMu.Lock()
				if j.to > maxDone {
					maxDone = j.to
					saveCheckpoint(db, jobName, maxDone)
				}
				maxDoneMu.Unlock()

				fmt.Printf("  [w%d] done %d-%d | total transfers: %d\n", workerID, j.from, j.to, atomic.LoadInt64(&totalInserted))
			}
		}(i)
	}

	wg.Wait()

	fmt.Printf("\n  Job [%s] complete! Total inserted: %d\n", jobName, atomic.LoadInt64(&totalInserted))
}
