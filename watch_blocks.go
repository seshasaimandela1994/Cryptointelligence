package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load(".env")
	rpcURL := os.Getenv("ETH_RPC_URL")
	ctx := context.Background()
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		fmt.Printf("Connection failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	fmt.Println()
	fmt.Println("  CryptoIntelligence - Live Block Watcher")
	fmt.Println("  Press Ctrl+C to stop.")
	fmt.Println()
	latestNum, _ := client.BlockNumber(ctx)
	fmt.Printf("  Connected - block #%d\n\n", latestNum)
	fmt.Printf("  %-14s %-22s %-6s %s\n", "BLOCK", "TIME (UTC)", "TXS", "BASE FEE")
	fmt.Println("  --------------------------------------------------")
	for {
		time.Sleep(6 * time.Second)
		currentNum, err := client.BlockNumber(ctx)
		if err != nil {
			continue
		}
		if currentNum <= latestNum {
			continue
		}
		for n := latestNum + 1; n <= currentNum; n++ {
			header, err := client.HeaderByNumber(ctx, new(big.Int).SetUint64(n))
			if err != nil {
				continue
			}
			blockTime := time.Unix(int64(header.Time), 0).UTC()
			baseFee := "n/a"
			if header.BaseFee != nil {
				gwei := new(big.Float).SetInt(header.BaseFee)
				gwei.Quo(gwei, big.NewFloat(1e9))
				v, _ := gwei.Float64()
				baseFee = fmt.Sprintf("%.3f gwei", v)
			}
			txCount, _ := client.TransactionCount(ctx, header.Hash())
			fmt.Printf("  #%-13d %-22s %-6d %s\n",
				n, blockTime.Format("2006-01-02 15:04:05"),
				txCount, baseFee)
		}
		latestNum = currentNum
	}
}
