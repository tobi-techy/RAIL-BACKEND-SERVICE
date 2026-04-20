package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// WarrantyRepository queries receipt_scans for high-value items and computes warranty status.
type WarrantyRepository struct {
	db *sqlx.DB
}

func NewWarrantyRepository(db *sqlx.DB) *WarrantyRepository {
	return &WarrantyRepository{db: db}
}

func (r *WarrantyRepository) GetWarrantyItems(ctx context.Context, userID uuid.UUID) ([]entities.WarrantyItem, error) {
	var scans []*entities.ReceiptScan
	err := r.db.SelectContext(ctx, &scans, `
		SELECT id, user_id, merchant, amount, currency, receipt_date, category, items, created_at
		FROM receipt_scans WHERE user_id = $1 AND jsonb_typeof(items) = 'array' AND jsonb_array_length(items) > 0
		ORDER BY created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, fmt.Errorf("get warranty scans: %w", err)
	}

	var result []entities.WarrantyItem
	for _, scan := range scans {
		purchaseDate := scan.CreatedAt
		if scan.ReceiptDate != nil {
			purchaseDate = *scan.ReceiptDate
		}

		var items []struct {
			Name  string `json:"name"`
			Price string `json:"price"`
		}
		if len(scan.Items) > 0 {
			if err := json.Unmarshal(scan.Items, &items); err != nil {
				continue
			}
		}

		for _, item := range items {
			if !isHighValue(item.Price) {
				continue
			}
			cat, months := categorizeItem(item.Name)
			expiry, days, status := computeWarrantyStatus(purchaseDate, months)

			result = append(result, entities.WarrantyItem{
				ItemName:       item.Name,
				PurchaseDate:   purchaseDate.Format("2006-01-02"),
				Merchant:       scan.Merchant,
				Price:          item.Price,
				Currency:       scan.Currency,
				Category:       cat,
				WarrantyMonths: months,
				ExpiryDate:     expiry,
				DaysRemaining:  days,
				Status:         status,
				ReceiptID:      scan.ID.String(),
			})
		}
	}

	sortWarrantyItems(result)
	return result, nil
}

func isHighValue(price string) bool {
	return parsePrice(price) > 50
}

func parsePrice(s string) float64 {
	var clean []byte
	dot := false
	for _, ch := range []byte(s) {
		if ch >= '0' && ch <= '9' {
			clean = append(clean, ch)
		} else if ch == '.' && !dot {
			clean = append(clean, ch)
			dot = true
		}
	}
	if len(clean) == 0 {
		return 0
	}
	var result, fracDiv float64
	pastDot := false
	for _, ch := range clean {
		if ch == '.' {
			pastDot = true
			continue
		}
		if pastDot {
			fracDiv++
			result += float64(ch-'0') / math.Pow(10, fracDiv)
		} else {
			result = result*10 + float64(ch-'0')
		}
	}
	return result
}

func categorizeItem(name string) (string, int) {
	lower := strings.ToLower(name)
	for _, kw := range []string{"laptop", "phone", "tv", "television", "headphone", "earphone", "airpod",
		"macbook", "ipad", "tablet", "monitor", "speaker", "camera", "console", "computer", "watch"} {
		if strings.Contains(lower, kw) {
			return "electronics", 12
		}
	}
	for _, kw := range []string{"fridge", "refrigerator", "washer", "washing", "dryer", "microwave",
		"dishwasher", "oven", "blender", "air conditioner", "vacuum"} {
		if strings.Contains(lower, kw) {
			return "appliance", 24
		}
	}
	for _, kw := range []string{"shirt", "shoe", "sneaker", "dress", "jacket", "coat", "trouser",
		"jean", "skirt", "boot", "sandal", "hoodie", "sweater"} {
		if strings.Contains(lower, kw) {
			return "clothing", 1
		}
	}
	return "general", 12
}

func computeWarrantyStatus(purchaseDate time.Time, warrantyMonths int) (string, int, string) {
	var expiry time.Time
	if warrantyMonths == 1 {
		expiry = purchaseDate.AddDate(0, 0, 30)
	} else {
		expiry = purchaseDate.AddDate(0, warrantyMonths, 0)
	}
	days := int(math.Ceil(time.Until(expiry).Hours() / 24))
	status := "active"
	if days <= 0 {
		status = "expired"
		days = 0
	} else if days <= 30 {
		status = "expiring_soon"
	}
	return expiry.Format("2006-01-02"), days, status
}

func sortWarrantyItems(items []entities.WarrantyItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			a, b := items[j], items[j-1]
			if a.Status == "expired" && b.Status != "expired" {
				break
			}
			if a.Status != "expired" && b.Status == "expired" {
				items[j], items[j-1] = items[j-1], items[j]
				continue
			}
			if a.DaysRemaining < b.DaysRemaining {
				items[j], items[j-1] = items[j-1], items[j]
			} else {
				break
			}
		}
	}
}
