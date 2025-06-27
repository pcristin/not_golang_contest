package api

import (
	"sync"

	"github.com/pcristin/golang_contest/internal/config"
	"github.com/pcristin/golang_contest/internal/database"
)

// Handler is the main handler for the API
type Handler struct {
	Config *config.Config

	// Clients
	Redis    *database.RedisClient
	Postgres *database.PostgresClient

	// Channels
	attemptsChan  chan database.CheckoutAttempt
	purchasesChan chan database.Purchase

	// Sale cached data
	saleCache sync.Map // key: saleID, value: SaleData
}

// NewHandler creates a new Handler
func NewHandler(config *config.Config, redis *database.RedisClient, postgres *database.PostgresClient) *Handler {
	return &Handler{
		Config:   config,
		Redis:    redis,
		Postgres: postgres,

		attemptsChan:  make(chan database.CheckoutAttempt, 100000), // approx 10 Mb of size
		purchasesChan: make(chan database.Purchase, 100000),        // approx 10 Mb of size
	}
}

// CheckoutResponse is the response for the checkout endpoint
type CheckoutResponse struct {
	Code string `json:"code"`
}

// PurchaseResponse is the response for the purchase endpoint
type PurchaseResponse struct {
	Status   string `json:"status"`
	ItemID   string `json:"item_id"`
	ItemName string `json:"item_name"`
	ImageURL string `json:"image_url"`
	Metadata string `json:"metadata"`
}

// SaleData is the data for a sale consisting of item name and image URL for
// the current sale
type SaleData struct {
	ItemName string
	ImageURL string
}

// HealthStatus represents the system health and statistics
type HealthStatus struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`

	// Service Health
	Services map[string]string `json:"services"`

	// Current Sale Info
	Sale SaleInfo `json:"sale"`

	// Performance Stats
	Performance PerformanceStats `json:"performance"`
}

// SaleInfo contains current sale information
type SaleInfo struct {
	ID       int    `json:"id"`
	ItemName string `json:"item_name,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Stock    int64  `json:"stock_remaining"`
	Sold     int64  `json:"items_sold"`
	Active   bool   `json:"is_active"`
}

// PerformanceStats contains performance metrics
type PerformanceStats struct {
	AttemptQueueSize  int `json:"attempt_queue_size"`
	PurchaseQueueSize int `json:"purchase_queue_size"`
	QueueCapacity     struct {
		Attempts  int `json:"attempts_max"`
		Purchases int `json:"purchases_max"`
	} `json:"queue_capacity"`
}
