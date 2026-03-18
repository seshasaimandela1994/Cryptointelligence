#!/usr/bin/env python3
# ============================================================
# CryptoIntelligence — Phase D: ML Classifier
# Random Forest classifier for 10 wallet behavior classes
# ============================================================

import psycopg2
import pandas as pd
import numpy as np
from sklearn.ensemble import RandomForestClassifier
from sklearn.model_selection import train_test_split
from sklearn.metrics import classification_report, accuracy_score
from sklearn.preprocessing import LabelEncoder
import json
import os

DB_URL = os.environ.get('DATABASE_URL')

print("CryptoIntelligence — ML Classifier v1")
print("=" * 50)

# ── Step 1: Load features ─────────────────────────────────
print("\nStep 1: Loading features from database...")
conn = psycopg2.connect(DB_URL)

df = pd.read_sql("""
    SELECT
        f.address,
        f.tx_count_total,
        f.unique_receivers,
        f.active_days_count,
        f.avg_tx_per_active_day,
        f.pct_interactions_dex,
        f.pct_interactions_cex,
        f.pct_interactions_mixer,
        f.pct_interactions_defi,
        f.pct_interactions_bridge,
        f.mev_pattern_score,
        f.wash_trade_score,
        f.flash_loan_score,
        f.mixer_exposure_score,
        f.in_degree,
        f.out_degree,
        f.cluster_size,
        f.has_kyc_link,
        f.sanctions_exposure,
        f.behavior_class
    FROM address_features_ml f
    WHERE f.behavior_class IS NOT NULL
    AND f.behavior_class != 'unclassified'
    AND f.tx_count_total > 0
""", conn)

print(f"  Loaded {len(df)} addresses with features")
print(f"  Classes: {df['behavior_class'].nunique()}")
print(f"  Class distribution:")
for cls, cnt in df['behavior_class'].value_counts().items():
    print(f"    {cls:<30} {cnt:>8}")

# ── Step 2: Prepare features ──────────────────────────────
print("\nStep 2: Preparing features...")

feature_cols = [
    'tx_count_total', 'unique_receivers', 'active_days_count',
    'avg_tx_per_active_day', 'pct_interactions_dex',
    'pct_interactions_cex', 'pct_interactions_mixer',
    'pct_interactions_defi', 'pct_interactions_bridge',
    'mev_pattern_score', 'wash_trade_score', 'flash_loan_score',
    'mixer_exposure_score', 'in_degree', 'out_degree',
    'cluster_size'
]

# Convert booleans
df['has_kyc_link'] = df['has_kyc_link'].astype(int)
df['sanctions_exposure'] = df['sanctions_exposure'].astype(int)
feature_cols += ['has_kyc_link', 'sanctions_exposure']

X = df[feature_cols].fillna(0)
y = df['behavior_class']

# Encode labels
le = LabelEncoder()
y_encoded = le.fit_transform(y)

print(f"  Features: {len(feature_cols)}")
print(f"  Training samples: {len(X)}")

# ── Step 3: Train/test split ──────────────────────────────
print("\nStep 3: Splitting train/test (80/20)...")
X_train, X_test, y_train, y_test = train_test_split(
    X, y_encoded, test_size=0.2, random_state=42, stratify=y_encoded
)
print(f"  Train: {len(X_train)}  Test: {len(X_test)}")

# ── Step 4: Train Random Forest ───────────────────────────
print("\nStep 4: Training Random Forest classifier...")
clf = RandomForestClassifier(
    n_estimators=100,
    max_depth=15,
    min_samples_leaf=5,
    random_state=42,
    n_jobs=-1
)
clf.fit(X_train, y_train)
print("  ✓ Model trained")

# ── Step 5: Evaluate ──────────────────────────────────────
print("\nStep 5: Evaluating model...")
y_pred = clf.predict(X_test)
accuracy = accuracy_score(y_test, y_pred)
print(f"  Accuracy: {accuracy:.4f} ({accuracy*100:.1f}%)")
print()
print("  Classification Report:")
print(classification_report(
    y_test, y_pred,
    target_names=le.classes_,
    zero_division=0
))

# ── Step 6: Feature importance ────────────────────────────
print("\nStep 6: Feature Importance:")
print("  " + "-" * 40)
importances = clf.feature_importances_
for feat, imp in sorted(zip(feature_cols, importances),
                         key=lambda x: x[1], reverse=True):
    bar = "█" * int(imp * 100)
    print(f"  {feat:<35} {imp:.4f} {bar}")

# ── Step 7: Run predictions on all addresses ──────────────
print("\nStep 7: Running predictions on all addresses...")
X_all = df[feature_cols].fillna(0)
proba_all = clf.predict_proba(X_all)
pred_all = clf.predict(X_all)

# ── Step 8: Store predictions in DB ──────────────────────
print("\nStep 8: Storing predictions in database...")
cur = conn.cursor()

# Clear old predictions
cur.execute("DELETE FROM ai_label_predictions WHERE model_name = 'RandomForest_v1'")

inserted = 0
for i, (_, row) in enumerate(df.iterrows()):
    pred_class = le.classes_[pred_all[i]]
    prob_score = float(proba_all[i][pred_all[i]])
    confidence = round(prob_score * 100, 2)

    # Confidence calibration formula
    evidence_strength = 90 if row['mev_pattern_score'] > 50 else 70
    graph_support = 80 if row['cluster_size'] > 1 else 50
    cluster_support = 80 if row['cluster_size'] > 5 else 60

    final_confidence = round(
        (prob_score * 0.5) * 100 +
        (evidence_strength * 0.3) +
        (graph_support * 0.1) +
        (cluster_support * 0.1), 2
    )
    final_confidence = min(100, final_confidence)

    # Top 3 feature contributions
    top_features = sorted(
        zip(feature_cols, importances * X_all.iloc[i].values),
        key=lambda x: abs(x[1]), reverse=True
    )[:3]

    explanation = {
        "predicted_class": pred_class,
        "probability": round(prob_score, 5),
        "top_features": [
            {"feature": f, "contribution": round(float(c), 4)}
            for f, c in top_features
        ],
        "calibrated_confidence": final_confidence
    }

    try:
        cur.execute("""
            INSERT INTO ai_label_predictions
                (address, model_name, model_version,
                 predicted_label_value, probability_score,
                 confidence_score, explanation_json, feature_version)
            VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
        """, (
            row['address'],
            'RandomForest_v1',
            'v1.0',
            pred_class,
            round(prob_score, 5),
            final_confidence,
            json.dumps(explanation),
            'v1'
        ))
        inserted += 1
    except Exception as e:
        pass

    if inserted % 10000 == 0 and inserted > 0:
        conn.commit()
        print(f"  ... {inserted} predictions stored")

conn.commit()
print(f"  ✓ {inserted} predictions stored")

# ── Step 9: Summary ───────────────────────────────────────
cur.execute("""
    SELECT predicted_label_value,
        COUNT(*) as count,
        ROUND(AVG(confidence_score)::numeric, 1) as avg_confidence
    FROM ai_label_predictions
    WHERE model_name = 'RandomForest_v1'
    GROUP BY predicted_label_value
    ORDER BY count DESC
""")

print("\nPrediction Distribution:")
print("  " + "-" * 50)
for row in cur.fetchall():
    print(f"  {row[0]:<30} {row[1]:>8}  conf: {row[2]}%")

cur.close()
conn.close()
print("\n✓ Phase D ML Classifier complete!")
