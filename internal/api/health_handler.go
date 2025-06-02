package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Health returns the health status and system statistics
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Initialize health status
	health := HealthStatus{
		Status:    "healthy",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Services:  make(map[string]string),
	}

	// Check service health
	health.Services["redis"] = h.checkRedisHealth(ctx)
	health.Services["postgres"] = h.checkPostgresHealth()

	// Determine overall status
	for _, status := range health.Services {
		if status != "healthy" {
			health.Status = "degraded"
			break
		}
	}

	// Get current sale info
	health.Sale = h.getCurrentSaleInfo(ctx)

	// Get performance stats
	health.Performance = h.getPerformanceStats()

	// Set appropriate status code
	statusCode := http.StatusOK
	if health.Status == "degraded" {
		statusCode = http.StatusServiceUnavailable
	}

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(health); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// checkRedisHealth checks if Redis is healthy
func (h *Handler) checkRedisHealth(ctx context.Context) string {
	if err := h.Redis.HealthCheck(ctx); err != nil {
		return "unhealthy: " + err.Error()
	}
	return "healthy"
}

// checkPostgresHealth checks if Postgres is healthy
func (h *Handler) checkPostgresHealth() string {
	if err := h.Postgres.HealthCheck(); err != nil {
		return "unhealthy: " + err.Error()
	}
	return "healthy"
}

// getCurrentSaleInfo gets current sale information
func (h *Handler) getCurrentSaleInfo(ctx context.Context) SaleInfo {
	saleInfo := SaleInfo{
		Active: false,
	}

	// Get active sale ID
	activeSaleID, err := h.Redis.GetActiveSaleID(ctx)
	if err != nil {
		return saleInfo
	}

	saleInfo.ID = activeSaleID
	saleInfo.Active = true

	// Get stock information
	if stock, err := h.Redis.GetSaleCurrentStock(ctx); err == nil {
		saleInfo.Stock = stock
	}

	if sold, err := h.Redis.GetItemsSoldCount(ctx); err == nil {
		saleInfo.Sold = sold
	}

	// Get sale metadata from Postgres
	if itemName, imageURL, err := h.Postgres.GetSaleByID(activeSaleID); err == nil {
		saleInfo.ItemName = itemName
		saleInfo.ImageURL = imageURL
	}

	return saleInfo
}

// getPerformanceStats gets performance metrics
func (h *Handler) getPerformanceStats() PerformanceStats {
	return PerformanceStats{
		AttemptQueueSize:  len(h.attemptsChan),
		PurchaseQueueSize: len(h.purchasesChan),
		QueueCapacity: struct {
			Attempts  int `json:"attempts_max"`
			Purchases int `json:"purchases_max"`
		}{
			Attempts:  cap(h.attemptsChan),
			Purchases: cap(h.purchasesChan),
		},
	}
}
