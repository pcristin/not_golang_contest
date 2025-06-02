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

	// Generate a request ID
	requestID := utils.GenerateRequestID()

	// Create a new context with the request ID
	ctx := r.Context()

	ctx = context.WithValue(ctx, myLogger.RequestIDKey, requestID)

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

	defer func() {
		select {
		case h.attemptsChan <- attempt:
			// Sent to the background worker
		default:
			logger.Error("dropped attempt: channel full")
		}
	}()

	// Decrement the stock
	_, err = h.Redis.DecrementStockFastFail(ctx)
	if err != nil {
		logger.Error("failed to decrement stock", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Increment the user checkout count to avoid race conditions
	userCheckoutCount, err := h.Redis.IncrementUserCheckoutCount(ctx, userID)
	if err != nil {
		logger.Error("failed to increment user checkout count", "error", err)
		if _, err := h.Redis.IncrementStockFastFail(ctx); err != nil {
			logger.Error("failed to increment stock", "error", err)
		}
		if err := h.Redis.DecrementUserCheckoutCount(ctx, userID); err != nil {
			logger.Error("failed to decrement user checkout count", "error", err)
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if userCheckoutCount > 10 {
		// Send the attempt to the background worker
		attempt.Status = "user limit"

		// Decrement the user checkout count to avoid race conditions
		if err := h.Redis.DecrementUserCheckoutCount(ctx, userID); err != nil {
			logger.Error("failed to decrement user checkout count", "error", err)
		}

		// Increment the stock to avoid race conditions
		if _, err := h.Redis.IncrementStockFastFail(ctx); err != nil {
			logger.Error("failed to increment stock", "error", err)
		}

		http.Error(w, "user has already checked out 10 items", http.StatusTooManyRequests)
		return
	}

	// Validate the item ID
	providedItemID, err := strconv.ParseInt(itemID, 10, 64)
	if err != nil || providedItemID <= 0 {
		logger.Error("failed to parse item ID", "error", err)
		http.Error(w, "invalid item ID", http.StatusBadRequest)
		return
	}

	// Atomically increment the items sold count
	actualItemsSold, err := h.Redis.IncrementItemsSoldCount(ctx)
	if err != nil {
		logger.Error("failed to increment items sold count", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if actualItemsSold > 10000 {
		logger.Error("sale has reached the maximum number of items sold")
		if err := h.Redis.DecrementItemsSoldCount(ctx); err != nil {
			logger.Error("failed to decrement items sold count", "error", err)
		}
		if _, err := h.Redis.IncrementStockFastFail(ctx); err != nil {
			logger.Error("failed to increment stock", "error", err)
		}
		if err := h.Redis.DecrementUserCheckoutCount(ctx, userID); err != nil {
			logger.Error("failed to decrement user checkout count", "error", err)
		}
		http.Error(w, "stock sold out", http.StatusConflict)
		return
	}

	// Generate a checkout code
	checkoutCode := utils.GenerateCode()

	// Store the checkout code in Redis (TTL is 20 seconds)
	if err := h.Redis.SetCheckoutCode(ctx, userID, saleIDStr, itemID, checkoutCode, 20); err != nil {
		logger.Error("failed to set checkout code", "error", err)
		if _, err := h.Redis.IncrementStockFastFail(ctx); err != nil {
			logger.Error("failed to increment stock", "error", err)
		}
		if err := h.Redis.DecrementUserCheckoutCount(ctx, userID); err != nil {
			logger.Error("failed to decrement user checkout count", "error", err)
		}
		if err := h.Redis.DecrementItemsSoldCount(ctx); err != nil {
			logger.Error("failed to decrement items sold count", "error", err)
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Send the attempt to the background worker
	attempt.Status = "success"
	attempt.Code = &checkoutCode

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
