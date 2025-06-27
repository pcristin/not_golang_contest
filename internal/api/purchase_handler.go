package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/pcristin/golang_contest/internal/database"
	myLogger "github.com/pcristin/golang_contest/internal/logger"
)

func (h *Handler) Purchase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := myLogger.FromContext(ctx, "purchase_handler")
	// Check if the request method is POST
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the request body
	code := r.URL.Query().Get("code")

	logger.Debug("purchase | request received", "path", r.URL.Path, "method", r.Method, "code", code)

	if code == "" {
		logger.Warn("purchase | code is required")
		http.Error(w, "code is required", http.StatusBadRequest)
		return
	}

	// Get checkout data from Redis
	checkoutData, err := h.Redis.GetAndDeleteCheckoutCodeAtomically(ctx, code)
	if checkoutData == "" {
		logger.Info("purchase | invalid or expired code", "code", code)
		http.Error(w, "invalid or expired code", http.StatusNotFound)
		return
	}
	if err != nil {
		logger.Error("purchase | failed to get checkout data", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Parse JSON data from Redis
	var data map[string]string
	if err := json.Unmarshal([]byte(checkoutData), &data); err != nil {
		logger.Error("purchase | failed to parse checkout data", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	userID := data["user_id"]
	saleIDStr := data["sale_id"]
	itemID := data["item_id"]
	saleID, err := strconv.Atoi(saleIDStr)
	if err != nil {
		logger.Error("purchase | failed to convert sale ID to int", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Get sale data from cache
	saleData, ok := h.saleCache.Load(saleID)
	var itemName, imageURL string

	if !ok {
		logger.Error("purchase | sale data not found in cache. Requesting sale data from Postgres", "sale_id", saleID)
		var err error
		itemName, imageURL, err = h.Postgres.GetSaleByID(saleID)
		if err != nil {
			logger.Error("purchase | failed to get sale data from Postgres", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		h.saleCache.Store(saleID, SaleData{
			ItemName: itemName,
			ImageURL: imageURL,
		})
	} else {
		// Safe type assertion with error handling
		sale, ok := saleData.(SaleData)
		if !ok {
			logger.Error("purchase | invalid sale data type in cache", "sale_id", saleID, "data_type", fmt.Sprintf("%T", saleData))
			// Fallback to database
			var err error
			itemName, imageURL, err = h.Postgres.GetSaleByID(saleID)
			if err != nil {
				logger.Error("purchase | failed to get sale data from Postgres", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
		} else {
			itemName = sale.ItemName
			imageURL = sale.ImageURL
		}
	}

	defer func() {
		select {
		case h.purchasesChan <- database.Purchase{
			UserID:      userID,
			SaleID:      saleID,
			ItemID:      itemID,
			PurchasedAt: time.Now(),
		}:
			// Sent to the background worker
		default:
			logger.Error("dropped purchase: channel full")
		}
	}()

	logger.Info("purchase | purchase completed successfully", "user_id", userID, "item_id", itemID, "sale_id", saleID)

	metadata := ""
	if rand.Intn(100) < 1 {
		metadata = "b64 aHR0cHM6Ly9naXRodWIuY29tL3BjcmlzdGluL2ZpbmRfd2hhdHNfaGlkZGVu"
	}

	resp := PurchaseResponse{
		Status:   "success",
		ItemID:   itemID,
		ItemName: itemName,
		ImageURL: imageURL,
		Metadata: metadata,
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// ProcessExpiredCheckout processes expired checkout attempts in the background
func (h *Handler) ProcessExpiredCheckouts(ctx context.Context) {
	logger := myLogger.FromContext(ctx, "purchase_handler")

	ticker := time.NewTicker(10 * time.Second) // To check up every 10 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("purchase | background worker stopped")
			return
		case <-ticker.C:
			err := h.CleanupExpiredCheckouts(ctx)
			if err != nil {
				logger.Error("purchase | failed to cleanup expired checkout attempts", "error", err)
			}
		}
	}
}

// CleanupExpiredCheckouts cleans up expired checkout attempts
func (h *Handler) CleanupExpiredCheckouts(ctx context.Context) error {
	logger := myLogger.FromContext(ctx, "purchase_handler")

	// Get potentialy expired attempts (older than 50 seconds to be safe)
	attempts, err := h.Postgres.GetExpiredCheckoutAttempts(50 * time.Second)
	if err != nil {
		logger.Error("purchase | failed to get expired checkout attempts", "error", err)
		return err
	}

	if len(attempts) == 0 {
		logger.Debug("purchase | no expired checkout attempts found")
		return nil
	}

	var expiredIDs []int

	for _, attempt := range attempts {
		if attempt.Code == nil {
			continue // Skip attempts without codes
		}

		// Check if code still exists in Redis
		_, err := h.Redis.GetCheckoutCode(ctx, *attempt.Code)
		if err != nil {
			// Code doesn't exist = expired
			expiredIDs = append(expiredIDs, attempt.ID)
		}
	}

	if len(expiredIDs) == 0 {
		logger.Debug("purchase | no expired checkout attempts found")
		return nil
	}

	// Update database
	if err := h.Postgres.MarkAttemptsExpired(expiredIDs); err != nil {
		logger.Error("purchase | failed to mark attempts as expired", "error", err)
		return fmt.Errorf("failed to mark attempts as expired: %v", err)
	}

	logger.Info("expired checkouts | cleaned up expired attempts", "count", len(expiredIDs))
	return nil
}

// processPurchaseInserts processes the purchase inserts in background worker pattern
func (h *Handler) ProcessPurchaseInserts(ctx context.Context) {
	logger := myLogger.FromContext(ctx, "purchase_worker")

	batch := make([]database.Purchase, 0, 100)
	ticker := time.NewTicker(1 * time.Second)

	for {
		select {
		case <-ctx.Done():
			// Flush remaining inserts
			if len(batch) > 0 {
				logger.Debug("flushing batch", "count", len(batch))
				h.flushPurchaseBatch(ctx, batch)
			}
			logger.Debug("context done")
			return

		case purchase := <-h.purchasesChan:
			batch = append(batch, purchase)
			// Flush batch if it's full
			if len(batch) >= 100 {
				h.flushPurchaseBatch(ctx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			// Flush batch if it's not empty and it's time to flush
			if len(batch) > 0 {
				h.flushPurchaseBatch(ctx, batch)
				batch = batch[:0]
			}
		}
	}
}

// flushPurchaseBatch flushes the batch to the database
func (h *Handler) flushPurchaseBatch(ctx context.Context, batch []database.Purchase) {
	// Init loger for module
	logger := myLogger.FromContext(ctx, "purchase_worker")

	err := h.Postgres.BatchInsertPurchases(batch)
	if err != nil {
		for _, purchase := range batch {
			if err := h.Postgres.InsertPurchase(purchase.UserID, purchase.SaleID, purchase.ItemID); err != nil {
				logger.Error("purchase | failed to insert purchase", "error", err)
			}
		}
	}
}
