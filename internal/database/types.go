package database

import (
	"database/sql"
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
)

// RedisClient is a wrapper around the Redis client
type RedisClient struct {
	// Connection pool to handle multiple connections
	pool *redis.Pool

	// Cache current sale ID
	currentSaleID  int
	cachedSaleTime time.Time
	cacheMutex     sync.RWMutex
}

// PostgresClient is a wrapper around the Postgres client
type PostgresClient struct {
	// Connection pool to handle multiple connections
	db *sql.DB
}

// CheckoutAttempt is a struct for transactions representing a checkout attempt
type CheckoutAttempt struct {
	ID        int
	UserID    string
	SaleID    int
	ItemID    string
	Code      *string
	Status    string
	CreatedAt time.Time
}

// Purchase is a struct for transactions representing a purchase
type Purchase struct {
	ID          int
	UserID      string
	SaleID      int
	ItemID      string
	PurchasedAt time.Time
}
