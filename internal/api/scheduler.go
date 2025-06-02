package api

import (
	"context"
	"fmt"
	"time"

	myLogger "github.com/pcristin/golang_contest/internal/logger"
	"github.com/pcristin/golang_contest/internal/utils"
)

// StartSaleScheduler starts the sale scheduler exactly at :00 on the running machine
func (h *Handler) StartSaleScheduler(ctx context.Context) {
	logger := myLogger.FromContext(ctx, "sale_scheduler")
	logger.Info("sale scheduler | starting sale scheduler with recovery check")

	// Reovery check on startup
	if err := h.recoverSaleState(ctx); err != nil {
		logger.Error("sale scheduler | recovery failed, will retry", "error", err)
		// !!! DO NOT FAIL STARTUP, CONTINUE WITH NORMAL SCHEDULING !!!
	}

	// Calculate time until next hour boundary
	h.waitForNextHourAndStart(ctx)
}

// recoverSaleState checks if we need to start a new sale immediately
func (h *Handler) recoverSaleState(ctx context.Context) error {
	logger := myLogger.FromContext(ctx, "sale_scheduler")

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := h.tryRecoverSaleState(ctx); err != nil {
			logger.Error("sale scheduler | recovery attempt failed", "attempt", attempt, "max_retries", maxRetries, "error", err)
			if attempt == maxRetries {
				return err
			}
			time.Sleep(time.Duration(attempt) * time.Second) // Exponential backoff
			continue
		}
		return nil
	}
	return fmt.Errorf("failed to recover sale state after %d attempts", maxRetries)
}

// tryRecoverSaleState checks if we need to start a new sale immediately
func (h *Handler) tryRecoverSaleState(ctx context.Context) error {
	logger := myLogger.FromContext(ctx, "sale_scheduler")

	// Check when last sale started
	lastSaleStartTime, err := h.Postgres.GetLastSaleStartTime()
	if err != nil {
		return fmt.Errorf("failed to get last sale start time: %v", err)
	}
	// If no previous or last sale was more than 1 hour ago, start a new sale
	if lastSaleStartTime.IsZero() || time.Since(lastSaleStartTime) > time.Hour {
		return h.executeNewSale(ctx)
	}

	// Check if current sale is properly set up in Redis
	currentSaleID, err := h.Redis.GetActiveSaleID(ctx)
	if err != nil || currentSaleID == 0 {
		logger.Error("sale scheduler | Redis sale state missing, restoring....")
		// Get the active sale ID from the database
		activeSaleID, err := h.Postgres.GetActiveSaleID()
		if err != nil {
			return fmt.Errorf("failed to get active sale ID: %v", err)
		}
		// If no active sale in database, start a new sale
		if activeSaleID == 0 {
			return h.executeNewSale(ctx)
		}
		// Restore Redis state for existing sale
		return h.restoreRedisSaleState(ctx, activeSaleID)
	}
	logger.Info("sale scheduler | current sale is active", "sale_id", currentSaleID)
	return nil
}

// waitForNextHourAndStart waits until the next hour boundary and starts a new sale
func (h *Handler) waitForNextHourAndStart(ctx context.Context) {
	logger := myLogger.FromContext(ctx, "sale_scheduler")

	for {
		// Calculate time untill next :00 hour
		now := time.Now()
		nextHour := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+1, 0, 0, 0, now.Location())
		timeUntilNextHour := nextHour.Sub(now)

		logger.Info("sale scheduler | waiting until next hour", "time_until_next_hour", timeUntilNextHour, "next_hour", nextHour)

		// Wait until the next hour boundary
		timer := time.NewTimer(timeUntilNextHour)
		select {
		case <-timer.C:
			// Start a new sale
			h.startNewSaleWithRetries(ctx)
			// Continue to next hour
		case <-ctx.Done():
			timer.Stop()
			logger.Info("sale scheduler | context cancelled, stopping")
			return
		}
	}
}

// startNewSaleWithRetries starts a new sale with retries
func (h *Handler) startNewSaleWithRetries(ctx context.Context) {
	logger := myLogger.FromContext(ctx, "sale_scheduler")

	maxRetries := 5
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := h.executeNewSale(ctx); err != nil {
			logger.Error("sale scheduler | failed to start new sale", "attempt", attempt, "max_retries", maxRetries, "error", err)
			if attempt == maxRetries {
				logger.Error("sale scheduler | CRITICAL: failed to start new sale after max attempts", "max_retries", maxRetries)
				return
			}
			time.Sleep(time.Duration(attempt*2) * time.Second) // Exponential backoff
			continue
		}
		logger.Info("sale scheduler | new sale started successfully", "attempt", attempt, "max_retries", maxRetries)
		return
	}
}

// executeNewSale starts a new sale
func (h *Handler) executeNewSale(ctx context.Context) error {
	logger := myLogger.FromContext(ctx, "sale_scheduler")

	// 1. Generate a new sale ID and item details and cache the sale data
	saleID := generateSaleID()
	itemName, imageURL := utils.GenerateItem(saleID, time.Now())

	// 2. Insert the new sale into the database
	actualSaleID, err := h.Postgres.InsertSale(itemName, imageURL)
	if err != nil {
		return fmt.Errorf("failed to insert new sale: %v", err)
	}

	// 3. Cache the sale data
	h.saleCache.Store(actualSaleID, SaleData{
		ItemName: itemName,
		ImageURL: imageURL,
	})

	// 4. Update the Redis active sale pointer
	if err := h.Redis.UpdateActiveSalePointer(ctx, actualSaleID); err != nil {
		return fmt.Errorf("failed to update Redis active sale pointer: %v", err)
	}

	// 5. Create the new sale in Redis
	if err := h.Redis.CreateNewSaleKeys(ctx, actualSaleID); err != nil {
		return fmt.Errorf("failed to create new sale keys in Redis: %v", err)
	}

	// 6. Clean up the old sale in Redis
	if err := h.Redis.CleanupOldSaleData(ctx); err != nil {
		return fmt.Errorf("failed to cleanup old sale data in Redis: %v", err)
	}

	// 7. End any active sale (optional - won't fail if none exists)
	if err := h.endAnyActiveSale(ctx); err != nil {
		logger.Error("sale scheduler | failed to end any active sale", "error", err)
	}

	logger.Info("sale scheduler | new sale started successfully", "sale_id", actualSaleID)
	return nil
}

// endAnyActiveSale ends any active sale
func (h *Handler) endAnyActiveSale(ctx context.Context) error {
	logger := myLogger.FromContext(ctx, "sale_scheduler")

	activeSaleID, err := h.Postgres.GetActiveSaleID()
	if err != nil {
		return err
	}

	if activeSaleID == 0 {
		logger.Info("sale scheduler | no active sale found to end")
		return nil
	}
	logger.Info("sale scheduler | ending active sale", "sale_id", activeSaleID)
	return h.Postgres.EndSale(activeSaleID)
}

// generateSaleID generates a new sale ID
func generateSaleID() int {
	now := time.Now()
	return int(now.Year()*10000 + int(now.YearDay())*100 + now.Hour())
}

// restoreRedisSaleState restores the Redis state for a sale
func (h *Handler) restoreRedisSaleState(ctx context.Context, saleID int) error {
	logger := myLogger.FromContext(ctx, "sale_scheduler")

	// Get sale data from Postgres
	itemName, imageURL, err := h.Postgres.GetSaleByID(saleID)
	if err != nil {
		return fmt.Errorf("failed to get sale data from Postgres: %v", err)
	}

	// Store in cache
	h.saleCache.Store(saleID, SaleData{
		ItemName: itemName,
		ImageURL: imageURL,
	})

	logger.Info("sale scheduler | restoring Redis state for sale", "sale_id", saleID)
	return h.Redis.CreateNewSaleKeys(ctx, saleID)
}
