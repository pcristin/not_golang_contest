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

		attemptsChan:  make(chan database.CheckoutAttempt, 25000), // approx 2,5 Mb of size
		purchasesChan: make(chan database.Purchase, 10000),        // approx 1 Mb of size
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
