package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	_ = godotenv.Load(".env")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Println()
	fmt.Println("  CryptoIntelligence — Connection Tests")
	fmt.Println("  =======================================")
	fmt.Println()

	// Test 1: PostgreSQL
	fmt.Print("  [1/3] PostgreSQL ... ")
	dbURL := os.Getenv("DATABASE_URL")
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
	} else {
		if err := pool.Ping(ctx); err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
		} else {
			var count int
			_ = pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public'",
			).Scan(&count)
			fmt.Printf("✓  connected (%d tables)\n", count)
		}
		pool.Close()
	}

	// Test 2: Redis
	fmt.Print("  [2/3] Redis ......... ")
	redisURL := os.Getenv("REDIS_URL")
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
	} else {
		rdb := redis.NewClient(opt)
		result, err := rdb.Ping(ctx).Result()
		if err != nil || result != "PONG" {
			fmt.Printf("❌ FAILED: %v\n", err)
		} else {
			fmt.Println("✓  connected (PONG)")
		}
		rdb.Close()
	}

	// Test 3: Ethereum
	fmt.Print("  [3/3] Ethereum RPC .. ")
	rpcURL := os.Getenv("ETH_RPC_URL")
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		fmt.Printf("❌ FAILED: %v\n", err)
	} else {
		blockNum, err := client.BlockNumber(ctx)
		if err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
		} else {
			fmt.Printf("✓  connected (latest block #%d)\n", blockNum)
		}
		client.Close()
	}

	fmt.Println()
	fmt.Println("  ─────────────────────────────────────")
	fmt.Println("  ✓ All systems ready — Phase 1 complete!")
	fmt.Println()
}
