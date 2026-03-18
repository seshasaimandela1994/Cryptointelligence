#!/usr/bin/env python3
# ============================================================
# CryptoIntelligence — Etherscan Label Scraper
# Fetches public labels from Etherscan for known addresses
# ============================================================

import urllib.request
import json
import time
import psycopg2
import os

DB_URL = os.environ.get('DATABASE_URL')

# Known labelled addresses from Etherscan public data
# These are publicly available OSINT labels
ETHERSCAN_KNOWN_LABELS = [
    # Binance
    ("0xbe0eb53f46cd790cd13851d5eff43d12404d33e8", "Binance", "Binance 7"),
    ("0xf977814e90da44bfa03b6295a0616a897441acec", "Binance", "Binance Hot Wallet"),
    ("0x28c6c06298d514db089934071355e5743bf21d60", "Binance", "Binance 14"),
    ("0x21a31ee1afc51d94c2efccaa2092ad1028285549", "Binance", "Binance 15"),

    # Coinbase
    ("0x503828976d22510aad0201ac7ec88293211d23da", "Coinbase", "Coinbase 1"),
    ("0x3cd751e6b0078be393132286c442345e5dc49699", "Coinbase", "Coinbase 2"),
    ("0xddfabcdc4d8ffc6d5beaf154f18b778f892a0740", "Coinbase", "Coinbase 3"),

    # Kraken
    ("0x2910543af39aba0cd09dbb2d50200b3e800a63d2", "Kraken", "Kraken 1"),
    ("0x267be1c1d684f78cb4f6a176c4911b741e4ffdc0", "Kraken", "Kraken 2"),

    # FTX (collapsed)
    ("0x2faf487a4414fe77e2327f0bf4ae2a264a776ad2", "FTX", "FTX Exchange"),
    ("0xc098b2a3aa256d2140208c3de6543aaef5cd3a94", "FTX", "FTX Hot Wallet"),

    # Tornado Cash
    ("0x910cbd523d972eb0a6f4cae4618ad62622b39dbf", "TornadoCash", "Tornado Cash 100 ETH"),
    ("0xa160cdab225685da1d56aa342ad8841c3b53f291", "TornadoCash", "Tornado Cash 1 ETH"),
    ("0xd4b88df4d29f5cedd6857912842cff3b20c8cfa3", "TornadoCash", "Tornado Cash 0.1 ETH"),

    # Uniswap
    ("0x7a250d5630b4cf539739df2c5dacb4c659f2488d", "Uniswap", "Uniswap V2 Router"),
    ("0xe592427a0aece92de3edee1f18e0157c05861564", "Uniswap", "Uniswap V3 Router"),

    # Ethereum Foundation
    ("0xde0b295669a9fd93d5f28d9ec85e40f4cb697bae", "EthereumFoundation", "Ethereum Foundation"),

    # Vitalik
    ("0xd8da6bf26964af9d7eed9e03e53415d37aa96045", "Vitalik", "Vitalik Buterin"),

    # Lazarus Group
    ("0x7f367cc41522ce07553e823bf3be79a889debe1b", "Lazarus", "Lazarus Group DPRK"),

    # OpenSea
    ("0x00000000006c3852cbef3e08e8df289169ede581", "OpenSea", "OpenSea Seaport"),
    ("0x7be8076f4ea4a4ad08075c2508e481d6c946d12b", "OpenSea", "OpenSea Wyvern"),

    # Aave
    ("0x7d2768de32b0b80b7a3454c06bdac94a69ddc7a9", "Aave", "Aave V2 LendingPool"),
    ("0x87870bca3f3fd6335c3f4ce8392d69350b4fa4e2", "Aave", "Aave V3 Pool"),

    # Curve
    ("0xbebc44782c7db0a1a60cb6fe97d0b483032ff1c7", "Curve", "Curve 3Pool"),

    # Compound
    ("0x3d9819210a31b4961b30ef54be2aed79b9c9cd3b", "Compound", "Compound Comptroller"),
]

def insert_labels(labels):
    conn = psycopg2.connect(DB_URL)
    cur = conn.cursor()

    inserted = 0
    for address, source_tag, label in labels:
        try:
            cur.execute("""
                INSERT INTO entity_labels
                    (address, label, category, confidence_tier, source)
                VALUES (%s, %s, %s, %s, %s)
                ON CONFLICT (address, label) DO NOTHING
            """, (
                address.lower(),
                f"Etherscan: {label}",
                categorize(source_tag),
                1,
                'etherscan'
            ))

            # Also add to identity_links
            cur.execute("""
                INSERT INTO identity_links
                    (address, identity, platform, confidence, source, verified)
                VALUES (%s, %s, %s, %s, %s, %s)
                ON CONFLICT DO NOTHING
            """, (
                address.lower(),
                label,
                'etherscan',
                85,
                'etherscan_public',
                True
            ))

            inserted += 1
        except Exception as e:
            print(f"Error inserting {address}: {e}")

    conn.commit()
    cur.close()
    conn.close()
    return inserted

def categorize(tag):
    tag = tag.lower()
    if tag in ['binance','coinbase','kraken','ftx','okx','huobi']:
        return 'exchange'
    if tag in ['tornadocash']:
        return 'mixer'
    if tag in ['uniswap','aave','curve','compound']:
        return 'defi'
    if tag in ['lazarus']:
        return 'sanctions'
    if tag in ['vitalik']:
        return 'individual'
    if tag in ['ethereumfoundation']:
        return 'foundation'
    if tag in ['opensea']:
        return 'nft'
    return 'unknown'

if __name__ == '__main__':
    print("CryptoIntelligence — Etherscan Label Importer")
    print("=" * 50)
    print(f"Loading {len(ETHERSCAN_KNOWN_LABELS)} known labels...")

    inserted = insert_labels(ETHERSCAN_KNOWN_LABELS)
    print(f"Inserted: {inserted} new labels")

    # Show summary
    conn = psycopg2.connect(DB_URL)
    cur = conn.cursor()
    cur.execute("""
        SELECT source, COUNT(*) as count
        FROM entity_labels
        GROUP BY source
        ORDER BY count DESC
    """)
    print("\nEntity Labels by Source:")
    print("-" * 30)
    for row in cur.fetchall():
        print(f"  {row[0]:<20} {row[1]}")

    cur.execute("SELECT COUNT(*) FROM identity_links WHERE platform = 'etherscan'")
    etherscan_links = cur.fetchone()[0]
    print(f"\nEtherscan identity links: {etherscan_links}")

    cur.close()
    conn.close()
