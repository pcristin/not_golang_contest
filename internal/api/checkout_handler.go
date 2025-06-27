package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/pcristin/golang_contest/internal/database"
	myLogger "github.com/pcristin/golang_contest/internal/logger"
	"github.com/pcristin/golang_contest/internal/utils"
)

func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	// Get context with request ID (set by middleware)
	ctx := r.Context()

	// Init logger for module
	logger := myLogger.FromContext(ctx, "checkout")

	// Check if the request method is POST
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the request body
	parsedURL := r.URL.Query()
	userID := parsedURL.Get("user_id")
	itemID := parsedURL.Get("id")

	logger.Debug("request received", "path", r.URL.Path, "method", r.Method, "userID", userID, "id", itemID)

	// Check if user_id and id are present
	if userID == "" || itemID == "" {
		http.Error(w, "user_id and id are required", http.StatusBadRequest)
		return
	}

	// Check if the sale is active
	saleIDStr, err := h.Redis.GetSaleCurrentID(ctx)
	if err != nil {
		logger.Error("failed to get current sale ID", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if saleIDStr == "" {
		logger.Error("no sale is active")
		http.Error(w, "no sale is active", http.StatusBadRequest)
		return
	}

	saleID, err := strconv.Atoi(saleIDStr)
	if err != nil {
		logger.Error("failed to convert sale ID to int", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Create a new checkout attempt
	attempt := database.CheckoutAttempt{
		UserID:    userID,
		SaleID:    saleID,
		ItemID:    itemID,
		Code:      nil,
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	// Validate the item ID first (no need to hit Redis for invalid requests)
	providedItemID, err := strconv.ParseInt(itemID, 10, 64)
	if err != nil || providedItemID <= 0 {
		logger.Error("failed to parse item ID", "error", err)
		http.Error(w, "invalid item ID", http.StatusBadRequest)
		return
	}

	// Perform atomic checkout operation
	result, err := h.Redis.AtomicCheckout(ctx, userID)
	if err != nil {
		logger.Error("failed to perform atomic checkout", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Handle different checkout statuses
	switch result.Status {
	case database.CheckoutOutOfStock:
		attempt.Status = "out of stock"
		defer func() {
			select {
			case h.attemptsChan <- attempt:
				// Sent to the background worker
			default:
				logger.Error("dropped attempt: channel full")
			}
		}()
		logger.Info("checkout failed: out of stock", "stock_remaining", result.StockRemaining)
		http.Error(w, "stock sold out", http.StatusConflict)
		return

	case database.CheckoutUserLimitExceeded:
		attempt.Status = "user limit"
		defer func() {
			select {
			case h.attemptsChan <- attempt:
				// Sent to the background worker
			default:
				logger.Error("dropped attempt: channel full")
			}
		}()
		logger.Info("checkout failed: user limit exceeded", "user_count", result.UserCount)
		http.Error(w, "user has already checked out 10 items", http.StatusTooManyRequests)
		return

	case database.CheckoutSaleLimitExceeded:
		attempt.Status = "sale limit"
		defer func() {
			select {
			case h.attemptsChan <- attempt:
				// Sent to the background worker
			default:
				logger.Error("dropped attempt: channel full")
			}
		}()
		logger.Info("checkout failed: sale limit exceeded", "items_sold", result.ItemsSold)
		http.Error(w, "stock sold out", http.StatusConflict)
		return

	case database.CheckoutSuccess:
		// Continue with successful checkout
		logger.Info("checkout succeeded atomically",
			"user_id", userID,
			"stock_remaining", result.StockRemaining,
			"user_count", result.UserCount,
			"items_sold", result.ItemsSold)

	default:
		attempt.Status = "unknown error"
		defer func() {
			select {
			case h.attemptsChan <- attempt:
				// Sent to the background worker
			default:
				logger.Error("dropped attempt: channel full")
			}
		}()
		logger.Error("checkout failed: unknown status", "status", result.Status)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Generate a checkout code
	checkoutCode := utils.GenerateCode()

	// Store the checkout code in Redis (TTL is 20 seconds)
	if err := h.Redis.SetCheckoutCode(ctx, userID, saleIDStr, itemID, checkoutCode, 20); err != nil {
		logger.Error("failed to set checkout code", "error", err)

		// Rollback the atomic checkout operation
		if rollbackErr := h.Redis.AtomicRollback(ctx, userID); rollbackErr != nil {
			logger.Error("failed to rollback atomic checkout", "error", rollbackErr)
		}

		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Send the attempt to the background worker
	attempt.Status = "success"
	attempt.Code = &checkoutCode

	// Use defer to ensure attempt is logged even if response writing fails
	defer func() {
		select {
		case h.attemptsChan <- attempt:
			// Sent to the background worker
		default:
			logger.Error("dropped attempt: channel full")
		}
	}()

	// Return the checkout code
	response := CheckoutResponse{
		Code: checkoutCode,
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// processCheckoutAttempts processes the checkout attempts in background worker pattern
func (h *Handler) ProcessCheckoutAttempts(ctx context.Context) {
	// Init logger for module
	logger := myLogger.FromContext(ctx, "checkout_worker")

	batch := make([]database.CheckoutAttempt, 0, 100)
	ticker := time.NewTicker(1 * time.Second)

	for {
		select {
		case <-ctx.Done():
			// Flush remaining attempts
			if len(batch) > 0 {
				logger.Debug("flushing attempts", "count", len(batch))
				h.flushAttemptsBatch(ctx, batch)
			}
			logger.Debug("context done")
			return

		case attempt := <-h.attemptsChan:
			batch = append(batch, attempt)
			// Flush batch if it's full
			if len(batch) >= 100 {
				h.flushAttemptsBatch(ctx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			// Flush batch if it's not empty and it's time to flush
			if len(batch) > 0 {
				h.flushAttemptsBatch(ctx, batch)
				batch = batch[:0]
			}
		}
	}

}

// flushBatch flushes the batch to the database
func (h *Handler) flushAttemptsBatch(ctx context.Context, batch []database.CheckoutAttempt) {
	// Init loger for module
	logger := myLogger.FromContext(ctx, "checkout_worker")

	err := h.Postgres.BatchInsertAttempts(batch)
	if err != nil {
		for _, attempt := range batch {
			if err := h.Postgres.InsertSingleAttempt(attempt); err != nil {
				logger.Error("failed to insert checkout attempt", "error", err)
			}
		}
	}
}
