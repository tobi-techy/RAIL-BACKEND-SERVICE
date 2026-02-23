#!/bin/bash

# Quick script to check if your deposit was processed
# Run this against your Railway database

echo "Checking if your 20 USDC deposit was processed..."
echo ""
echo "Run these queries in your Railway Postgres console:"
echo ""
echo "1. Check deposits table:"
echo "   SELECT id, user_id, chain, tx_hash, amount, status, created_at"
echo "   FROM deposits"
echo "   WHERE tx_hash = '4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU';"
echo ""
echo "2. Check your user balance:"
echo "   SELECT * FROM ledger_entries"
echo "   WHERE user_id = (SELECT user_id FROM managed_wallets WHERE address = 'Bes4jEMuVKqj4m3MhQtXsZSqfKFMThYetkUjFjEyTPRZ')"
echo "   ORDER BY created_at DESC LIMIT 5;"
echo ""
echo "3. Check allocation (70/30 split):"
echo "   SELECT * FROM allocation_transactions"
echo "   WHERE user_id = (SELECT user_id FROM managed_wallets WHERE address = 'Bes4jEMuVKqj4m3MhQtXsZSqfKFMThYetkUjFjEyTPRZ')"
echo "   ORDER BY created_at DESC LIMIT 5;"
echo ""
