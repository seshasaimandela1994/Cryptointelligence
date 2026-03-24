#!/usr/bin/env python3
# ============================================================
# CryptoIntelligence — Wallet Prediction Engine
# Predicts next wallet action using behavioral patterns
# ============================================================

import os
import psycopg2
import pandas as pd
import numpy as np
from sklearn.ensemble import GradientBoostingClassifier
from sklearn.preprocessing import LabelEncoder
from sklearn.model_selection import train_test_split
from sklearn.metrics import accuracy_score, classification_report
import json
import warnings
warnings.filterwarnings('ignore')

DB_URL = os.environ.get('DATABASE_URL')
conn = psycopg2.connect(DB_URL)

print()
print("  CryptoIntelligence — Wallet Prediction Engine")
print("  ===============================================")
print()

# ── STEP 1: Load behavioral features ─────────────────────
print("  Step 1: Loading behavioral features...")
df = pd.read_sql("""
    SELECT
        ab.address,
        ab.total_transactions,
        ab.unique_tokens_used,
        ab.unique_destinations,
        ab.blocks_active,
        ab.tx_per_block,
        ab.dex_interactions,
        ab.exchange_interactions,
        ab.bridge_usage,
        ab.mixer_contacts,
        ab.sanctions_contacts,
        ab.nft_interactions,
        ab.same_block_txs,
        ab.behavior_class,
        COALESCE(rs.risk_score, 0) as risk_score,
        COALESCE(rep.reputation, 500) as reputation,
        COALESCE(ct.chain_count, 0) as chains_used
    FROM address_behaviors ab
    LEFT JOIN risk_scores rs ON ab.address = rs.address
    LEFT JOIN reputation_scores rep ON ab.address = rep.address
    LEFT JOIN (
        SELECT eth_address, COUNT(DISTINCT chain) as chain_count
        FROM crosschain_tracking
        GROUP BY eth_address
    ) ct ON ab.address = ct.eth_address
    WHERE ab.behavior_class IS NOT NULL
    AND ab.behavior_class != 'unclassified'
    LIMIT 50000
""", conn)

print(f"  ✓ Loaded {len(df):,} addresses with behavioral features")
print(f"  ✓ Features: {len(df.columns)} columns")
print()

# ── STEP 2: Define prediction targets ────────────────────
print("  Step 2: Defining prediction targets...")

def predict_next_action(row):
    """Predict what this wallet will do next"""
    if row['sanctions_contacts'] > 0 or row['mixer_contacts'] > 0:
        return 'USE_MIXER'
    if row['tx_per_block'] >= 3 and row['same_block_txs'] >= 3:
        return 'MEV_ARBITRAGE'
    if row['bridge_usage'] > 2 and row['chains_used'] > 1:
        return 'BRIDGE_FUNDS'
    if row['exchange_interactions'] >= 5:
        return 'EXIT_TO_EXCHANGE'
    if row['dex_interactions'] >= 10:
        return 'DEX_TRADE'
    if row['total_transactions'] > 100 and row['tx_per_block'] > 1:
        return 'HIGH_FREQUENCY_TRADE'
    if row['nft_interactions'] >= 3:
        return 'NFT_ACTIVITY'
    if row['total_transactions'] <= 5:
        return 'HOLD'
    return 'NORMAL_TRANSFER'

df['next_action'] = df.apply(predict_next_action, axis=1)

print("  Prediction target distribution:")
dist = df['next_action'].value_counts()
for action, count in dist.items():
    pct = count/len(df)*100
    print(f"    {action:<25} {count:>8,}  ({pct:.1f}%)")
print()

# ── STEP 3: Train prediction model ───────────────────────
print("  Step 3: Training prediction model...")

features = [
    'total_transactions', 'unique_tokens_used', 'unique_destinations',
    'blocks_active', 'tx_per_block', 'dex_interactions',
    'exchange_interactions', 'bridge_usage', 'mixer_contacts',
    'sanctions_contacts', 'nft_interactions', 'same_block_txs',
    'risk_score', 'reputation', 'chains_used'
]

X = df[features].fillna(0)
le = LabelEncoder()
y = le.fit_transform(df['next_action'])

X_train, X_test, y_train, y_test = train_test_split(
    X, y, test_size=0.2, random_state=42)

model = GradientBoostingClassifier(
    n_estimators=100, max_depth=5,
    learning_rate=0.1, random_state=42)
model.fit(X_train, y_train)

y_pred = model.predict(X_test)
accuracy = accuracy_score(y_test, y_pred)
print(f"  ✓ Model trained! Accuracy: {accuracy*100:.2f}%")
print()

# ── STEP 4: Feature importance ───────────────────────────
print("  Step 4: Feature importance (what drives predictions):")
importances = sorted(zip(features, model.feature_importances_),
    key=lambda x: x[1], reverse=True)
for feat, imp in importances[:8]:
    bar = '█' * int(imp * 100)
    print(f"    {feat:<28} {imp:.3f}  {bar}")
print()

# ── STEP 5: Store predictions ────────────────────────────
print("  Step 5: Storing predictions in database...")

cur = conn.cursor()
cur.execute("""
    CREATE TABLE IF NOT EXISTS wallet_predictions (
        address         CHAR(42) PRIMARY KEY,
        predicted_action VARCHAR(50),
        confidence      NUMERIC(5,2),
        risk_signal     VARCHAR(20),
        predicted_at    TIMESTAMP DEFAULT NOW()
    )
""")

# Predict for all addresses
proba = model.predict_proba(X.fillna(0))
predictions = le.inverse_transform(model.predict(X.fillna(0)))
confidences = proba.max(axis=1) * 100

stored = 0
for i, (_, row) in enumerate(df.iterrows()):
    action = predictions[i]
    conf = float(confidences[i])
    risk_signal = 'HIGH' if action in ['USE_MIXER', 'MEV_ARBITRAGE'] else \
                  'MEDIUM' if action in ['BRIDGE_FUNDS', 'EXIT_TO_EXCHANGE'] else 'LOW'
    try:
        cur.execute("""
            INSERT INTO wallet_predictions
                (address, predicted_action, confidence, risk_signal)
            VALUES (%s, %s, %s, %s)
            ON CONFLICT (address) DO UPDATE SET
                predicted_action = EXCLUDED.predicted_action,
                confidence = EXCLUDED.confidence,
                risk_signal = EXCLUDED.risk_signal,
                predicted_at = NOW()
        """, (row['address'], action, conf, risk_signal))
        stored += 1
    except:
        pass

conn.commit()
print(f"  ✓ {stored:,} predictions stored")
print()

# ── STEP 6: Interesting predictions ──────────────────────
print("  Step 6: High-risk predictions (wallets about to do something risky):")
print()

risky = pd.read_sql("""
    SELECT wp.address, wp.predicted_action,
        wp.confidence, wp.risk_signal,
        COALESCE(al.label, 'Unknown') as label,
        COALESCE(rs.risk_score, 0) as current_risk
    FROM wallet_predictions wp
    LEFT JOIN address_labels al ON wp.address = al.address
    LEFT JOIN risk_scores rs ON wp.address = rs.address
    WHERE wp.predicted_action IN ('USE_MIXER','MEV_ARBITRAGE','BRIDGE_FUNDS')
    AND wp.confidence > 80
    ORDER BY wp.confidence DESC, rs.risk_score DESC
    LIMIT 10
""", conn)

print(f"  {'ADDRESS':<14} {'PREDICTED ACTION':<22} {'CONF':>6} {'RISK':>5}  LABEL")
print("  " + "─"*75)
for _, r in risky.iterrows():
    addr = r['address'][:10] + '...' + r['address'][-4:]
    print(f"  {addr:<14} {r['predicted_action']:<22} {r['confidence']:>5.1f}% "
          f"{r['current_risk']:>4}  {r['label']}")

print()

# Summary
print("  ══════════════════════════════════════════════════")
print(f"  Prediction Engine Complete!")
print(f"  Model accuracy:      {accuracy*100:.2f}%")
print(f"  Addresses predicted: {stored:,}")
print(f"  High-risk signals:   {len(risky)}")
print("  ══════════════════════════════════════════════════")
print()

conn.close()
