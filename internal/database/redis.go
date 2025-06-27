package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
	myLogger "github.com/pcristin/golang_contest/internal/logger"
)

func NewRedisClient(ctx context.Context, address string) *RedisClient {
	logger := myLogger.FromContext(ctx, "redis")

	pool := &redis.Pool{
		MaxIdle:     1000,              // Max idle conns
		MaxActive:   2000,              // Max active conns
		IdleTimeout: 240 * time.Second, // Idle timeout

		// Wait for available connection
		Wait: true,

		// Duration of connection
		MaxConnLifetime: 10 * time.Minute, // Max lifetime of connection

		// Dial function creates a new connection when needed with timeout
		Dial: func() (redis.Conn, error) {
			logger.Info("redis | dialing", "address", address)
			return redis.Dial("tcp", address,
				redis.DialConnectTimeout(5*time.Second),
				redis.DialReadTimeout(3*time.Second),
				redis.DialWriteTimeout(3*time.Second),
			)
		},

		// Test if conn is still alive
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
	}
	return &RedisClient{
		pool: pool,
	}
}

// GetCheckoutCode retrieves a value from Redis
func (r *RedisClient) GetCheckoutCode(ctx context.Context, code string) (string, error) {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	reply, err := redis.String(conn.Do("GET", code))
	if err != nil {
		if err == redis.ErrNil {
			logger.Debug("redis get | checkout code not found", "code", code)
		} else {
			logger.Error("redis get | failed to get checkout code", "error", err)
		}
		return "", err
	}
	logger.Debug("redis get | got checkout code", "code", code)
	return reply, nil
}

// SetCheckoutCode stores a value in Redis with expiration
func (r *RedisClient) SetCheckoutCode(ctx context.Context, userID string, saleID string, itemID string, code string, expireSeconds int) error {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	// SETEX = SET with EXpiration
	jsonData, err := json.Marshal(map[string]string{"user_id": userID, "sale_id": saleID, "item_id": itemID, "created_at": time.Now().Format(time.RFC3339)})
	if err != nil {
		logger.Error("redis set | failed to marshal checkout data", "error", err)
		return err
	}
	_, err = conn.Do("SETEX", "checkout:"+code, expireSeconds, jsonData)
	if err != nil {
		logger.Error("redis set | failed to set checkout code", "error", err)
		return err
	}
	logger.Debug("redis set | set checkout code", "code", code, "user_id", userID)
	return err
}

// DecrementStock decrements a value (atomically!) in Redis and returns the new value
// DECR return value AFTER decrementing
// If it was 1, DECR will return 0
// If it was 0, DECR will return -1 (then the stock is fully sold out!)
func (r *RedisClient) DecrementStockFastFail(ctx context.Context) (int64, error) {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis decrement | failed to get active sale ID", "error", err)
		return 0, err
	}

	conn := r.pool.Get()
	defer conn.Close()

	reply, err := redis.Int64(conn.Do("DECR", fmt.Sprintf("sale:%d:stock", activeSaleID)))
	if err != nil {
		logger.Error("redis decrement | failed to decrement stock", "error", err)
		return 0, err
	}

	logger.Debug("redis decrement | decremented stock", "sale_id", activeSaleID, "stock", reply)
	return reply, nil
}

// IncrementStockFastFail increments a value (atomically!) in Redis and returns the new value
// INCR return value AFTER incrementing
// If it was 0, INCR will return 1
// If it was 1, INCR will return 2
func (r *RedisClient) IncrementStockFastFail(ctx context.Context) (int64, error) {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis increment | failed to get active sale ID", "error", err)
		return 0, err
	}

	conn := r.pool.Get()
	defer conn.Close()

	reply, err := redis.Int64(conn.Do("INCR", fmt.Sprintf("sale:%d:stock", activeSaleID)))
	if err != nil {
		logger.Error("redis increment | failed to increment stock", "error", err)
		return 0, err
	}

	logger.Debug("redis increment | incremented stock", "sale_id", activeSaleID, "stock", reply)
	return reply, nil
}

// HealthCheck checks if the Redis connection is alive
func (r *RedisClient) HealthCheck(ctx context.Context) error {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	_, err := conn.Do("PING")
	if err != nil {
		logger.Error("redis health check | failed to ping Redis", "error", err)
		return err
	}
	logger.Debug("redis health check | Redis connection is alive")
	return err
}

// GetUserCheckoutCount returns the number of items the user has checked out
func (r *RedisClient) GetUserCheckoutCount(ctx context.Context, userID string) (int64, error) {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	reply, err := redis.Int64(conn.Do("GET", "sale:current:user:"+userID+":count"))
	if err != nil {
		if err != redis.ErrNil {
			logger.Error("redis get | failed to get user checkout count", "error", err)
		}
		return 0, err
	}
	logger.Debug("redis get | got user checkout count", "user_id", userID, "count", reply)
	return reply, nil
}

// IncrementUserCheckoutCount increments the number of items the user has checked out
func (r *RedisClient) IncrementUserCheckoutCount(ctx context.Context, userID string) (int64, error) {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	count, err := redis.Int64(conn.Do("INCR", "sale:current:user:"+userID+":count"))
	if err != nil {
		logger.Error("redis increment | failed to increment user checkout count", "error", err)
		return 0, err
	}
	logger.Debug("redis increment | incremented user checkout count", "user_id", userID, "count", count)
	return count, nil
}

// DecrementUserCheckoutCount decrements the number of items the user has checked out
func (r *RedisClient) DecrementUserCheckoutCount(ctx context.Context, userID string) error {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	_, err := conn.Do("DECR", "sale:current:user:"+userID+":count")
	if err != nil {
		logger.Error("redis decrement | failed to decrement user checkout count", "error", err)
		return err
	}
	logger.Debug("redis decrement | decremented user checkout count", "user_id", userID)
	return err
}

// GetSaleCurrentID returns the current sale ID
func (r *RedisClient) GetSaleCurrentID(ctx context.Context) (string, error) {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis get | failed to get active sale ID", "error", err)
		return "", err
	}

	conn := r.pool.Get()
	defer conn.Close()

	reply, err := redis.String(conn.Do("GET", fmt.Sprintf("sale:%d:id", activeSaleID)))
	if err != nil {
		logger.Error("redis get | failed to get sale current ID", "error", err)
		return "", err
	}
	logger.Debug("redis get | got sale current ID", "sale_id", activeSaleID, "id", reply)
	return reply, nil
}

// GetSaleCurrentStock returns the current sale stock.
// This is the number of items that are available for purchase.
func (r *RedisClient) GetSaleCurrentStock(ctx context.Context) (int64, error) {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis get | failed to get active sale ID", "error", err)
		return 0, err
	}

	conn := r.pool.Get()
	defer conn.Close()

	reply, err := redis.Int64(conn.Do("GET", fmt.Sprintf("sale:%d:stock", activeSaleID)))
	if err != nil {
		logger.Error("redis get | failed to get sale current stock", "error", err)
		return 0, err
	}
	logger.Debug("redis get | got sale current stock", "sale_id", activeSaleID, "stock", reply)
	return reply, nil
}

// DeleteCode deletes a checkout code from Redis to prevent reuse
func (r *RedisClient) DeleteCode(ctx context.Context, code string) error {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	_, err := conn.Do("DEL", "checkout:"+code)
	if err != nil {
		logger.Error("redis delete | failed to delete checkout code", "error", err)
		return err
	}
	logger.Debug("redis delete | deleted checkout code", "code", code)
	return err
}

// GetItemsSoldCount returns the number of items sold
func (r *RedisClient) GetItemsSoldCount(ctx context.Context) (int64, error) {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		return 0, err
	}

	conn := r.pool.Get()
	defer conn.Close()

	soldKey := fmt.Sprintf("sale:%d:items_sold", activeSaleID)

	reply, err := redis.Int64(conn.Do("GET", soldKey))
	if err != nil {
		if err != redis.ErrNil {
			logger.Error("redis get | failed to get items sold count", "error", err)
		}
		return 0, err
	}
	logger.Debug("redis get | got items sold count", "sale_id", activeSaleID, "count", reply)
	return reply, nil
}

// IncrementItemsSoldCount increments the number of items sold
func (r *RedisClient) IncrementItemsSoldCount(ctx context.Context) (int64, error) {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis increment | failed to get active sale ID", "error", err)
		return 0, err
	}

	// Increment the items sold count
	conn := r.pool.Get()
	defer conn.Close()

	soldKey := fmt.Sprintf("sale:%d:items_sold", activeSaleID)

	reply, err := redis.Int64(conn.Do("INCR", soldKey))
	if err != nil {
		logger.Error("redis increment | failed to increment items sold count", "error", err)
		return 0, err
	}

	logger.Info("redis increment | incremented items sold count", "sale_id", activeSaleID, "count", reply)
	return reply, nil
}

// DecrementItemsSoldCount decrements the number of items sold
func (r *RedisClient) DecrementItemsSoldCount(ctx context.Context) error {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis decrement | failed to get active sale ID", "error", err)
		return err
	}

	conn := r.pool.Get()
	defer conn.Close()

	_, err = conn.Do("DECR", fmt.Sprintf("sale:%d:items_sold", activeSaleID))
	if err != nil {
		logger.Error("redis decrement | failed to decrement items sold count", "error", err)
		return err
	}
	logger.Debug("redis decrement | decremented items sold count", "sale_id", activeSaleID)
	return err
}

// getActiveSaleID returns the ID of the active sale
func (r *RedisClient) GetActiveSaleID(ctx context.Context) (int, error) {
	logger := myLogger.FromContext(ctx, "redis")

	// Check if the current sale ID is cached and if it's less than 1 minute old
	r.cacheMutex.RLock()
	if r.currentSaleID != 0 && time.Since(r.cachedSaleTime) < 1*time.Hour {
		logger.Debug("redis get | got active sale ID from cache", "sale_id", r.currentSaleID)
		r.cacheMutex.RUnlock()
		return r.currentSaleID, nil
	}
	r.cacheMutex.RUnlock()

	conn := r.pool.Get()
	defer conn.Close()

	// Get active sale ID from pointer
	activeSaleID, err := redis.Int(conn.Do("GET", "sale:current:active_sale"))
	if err != nil {
		logger.Error("redis get | no active sale found", "error", err)
		return 0, fmt.Errorf("no active sale found: %v", err)
	}
	logger.Debug("redis get | got active sale ID", "sale_id", activeSaleID)

	// Cache the active sale ID
	r.cacheMutex.Lock()
	r.currentSaleID = activeSaleID
	r.cachedSaleTime = time.Now()
	r.cacheMutex.Unlock()
	return activeSaleID, nil
}

// CleanupOldSaleData cleans up the old sale data
func (r *RedisClient) CleanupOldSaleData(ctx context.Context) error {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	// Delete all user count keys
	userKeys, err := redis.Strings(conn.Do("KEYS", "sale:current:user:*:count"))
	if err != nil {
		return fmt.Errorf("failed to get user count keys: %v", err)
	}

	if len(userKeys) > 0 {
		// Convert to []interface{} for DEL command
		args := make([]interface{}, len(userKeys))
		for i, key := range userKeys {
			args[i] = key
		}
		_, err = conn.Do("DEL", args...)
		if err != nil {
			return fmt.Errorf("failed to delete user count keys: %v", err)
		}
		logger.Info("redis cleanup | deleted user count keys", "count", len(userKeys))
	}

	// Delete all checkout code keys
	checkoutKeys, err := redis.Strings(conn.Do("KEYS", "checkout:*"))
	if err != nil {
		return fmt.Errorf("failed to get checkout keys: %v", err)
	}

	if len(checkoutKeys) > 0 {
		// Convert to []interface{} for DEL command
		args := make([]interface{}, len(checkoutKeys))
		for i, key := range checkoutKeys {
			args[i] = key
		}
		_, err = conn.Do("DEL", args...)
		if err != nil {
			return fmt.Errorf("failed to delete checkout keys: %v", err)
		}
		logger.Info("redis cleanup | deleted checkout keys", "count", len(checkoutKeys))
	}

	logger.Info("redis cleanup | cleanup completed successfully")
	return nil
}

// createNewSaleKeys creates versioned sale keys for a new sale
func (r *RedisClient) CreateNewSaleKeys(ctx context.Context, newSaleID int) error {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	err := conn.Send("MULTI")
	if err != nil {
		return err
	}

	// Create versioned sale keys (1 hour TTL)
	err = conn.Send("SETEX", fmt.Sprintf("sale:%d:id", newSaleID), 3600, newSaleID)
	if err != nil {
		return err
	}

	err = conn.Send("SETEX", fmt.Sprintf("sale:%d:stock", newSaleID), 3600, 10000)
	if err != nil {
		return err
	}

	err = conn.Send("SETEX", fmt.Sprintf("sale:%d:items_sold", newSaleID), 3600, 0)
	if err != nil {
		return err
	}

	err = conn.Send("SETEX", fmt.Sprintf("sale:%d:started_at", newSaleID), 3600, time.Now().Unix())
	if err != nil {
		return err
	}

	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}

	logger.Info("redis creation | created versioned sale keys for sale ID", "sale_id", newSaleID)
	return nil
}

// Close closes the Redis connection
func (r *RedisClient) Close() error {
	return r.pool.Close()
}

// AtomicCheckout performs all checkout validations and counter updates atomically using Lua script
func (r *RedisClient) AtomicCheckout(ctx context.Context, userID string) (*CheckoutResult, error) {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis atomic checkout | failed to get active sale ID", "error", err)
		return nil, err
	}

	conn := r.pool.Get()
	defer conn.Close()

	// Prepare keys and arguments
	stockKey := fmt.Sprintf("sale:%d:stock", activeSaleID)
	userCountKey := fmt.Sprintf("sale:current:user:%s:count", userID)
	itemsSoldKey := fmt.Sprintf("sale:%d:items_sold", activeSaleID)

	keys := []interface{}{stockKey, userCountKey, itemsSoldKey}
	args := []interface{}{userID, 10, 10000} // max_user_items=10, max_total_items=10000

	// Execute Lua script
	result, err := redis.Ints(conn.Do("EVAL", AtomicCheckoutScript, 3, keys[0], keys[1], keys[2], args[0], args[1], args[2]))
	if err != nil {
		logger.Error("redis atomic checkout | failed to execute script", "error", err)
		return nil, err
	}

	if len(result) != 4 {
		logger.Error("redis atomic checkout | unexpected result length", "length", len(result))
		return nil, fmt.Errorf("unexpected result length: %d", len(result))
	}

	checkoutResult := &CheckoutResult{
		StockRemaining: int64(result[0]),
		UserCount:      int64(result[1]),
		ItemsSold:      int64(result[2]),
		Status:         CheckoutStatus(result[3]),
	}

	logger.Debug("redis atomic checkout | completed",
		"user_id", userID,
		"stock_remaining", checkoutResult.StockRemaining,
		"user_count", checkoutResult.UserCount,
		"items_sold", checkoutResult.ItemsSold,
		"status", checkoutResult.Status.String())

	return checkoutResult, nil
}

// AtomicRollback rolls back a failed checkout atomically using Lua script
func (r *RedisClient) AtomicRollback(ctx context.Context, userID string) error {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis atomic rollback | failed to get active sale ID", "error", err)
		return err
	}

	conn := r.pool.Get()
	defer conn.Close()

	// Prepare keys
	stockKey := fmt.Sprintf("sale:%d:stock", activeSaleID)
	userCountKey := fmt.Sprintf("sale:current:user:%s:count", userID)
	itemsSoldKey := fmt.Sprintf("sale:%d:items_sold", activeSaleID)

	keys := []interface{}{stockKey, userCountKey, itemsSoldKey}
	args := []interface{}{userID}

	// Execute Lua script
	result, err := redis.Ints(conn.Do("EVAL", AtomicRollbackScript, 3, keys[0], keys[1], keys[2], args[0]))
	if err != nil {
		logger.Error("redis atomic rollback | failed to execute script", "error", err)
		return err
	}

	logger.Debug("redis atomic rollback | completed",
		"user_id", userID,
		"new_stock", result[0],
		"new_user_count", result[1],
		"new_items_sold", result[2])

	return nil
}

// AtomicCleanupExpiredCheckout cleans up expired checkout and updates counters atomically
func (r *RedisClient) AtomicCleanupExpiredCheckout(ctx context.Context, userID string) error {
	logger := myLogger.FromContext(ctx, "redis")

	// Get the active sale ID
	activeSaleID, err := r.GetActiveSaleID(ctx)
	if err != nil {
		logger.Error("redis atomic cleanup | failed to get active sale ID", "error", err)
		return err
	}

	conn := r.pool.Get()
	defer conn.Close()

	// Prepare keys
	stockKey := fmt.Sprintf("sale:%d:stock", activeSaleID)
	userCountKey := fmt.Sprintf("sale:current:user:%s:count", userID)
	itemsSoldKey := fmt.Sprintf("sale:%d:items_sold", activeSaleID)

	keys := []interface{}{stockKey, userCountKey, itemsSoldKey}
	args := []interface{}{userID}

	// Execute Lua script
	result, err := redis.Ints(conn.Do("EVAL", AtomicCleanupExpiredCheckoutScript, 3, keys[0], keys[1], keys[2], args[0]))
	if err != nil {
		logger.Error("redis atomic cleanup | failed to execute script", "error", err)
		return err
	}

	logger.Debug("redis atomic cleanup | completed",
		"user_id", userID,
		"new_stock", result[0],
		"new_user_count", result[1],
		"new_items_sold", result[2])

	return nil
}

// AtomicInitializeSale initializes all counters for a new sale atomically
func (r *RedisClient) AtomicInitializeSale(ctx context.Context, saleID int, initialStock int) error {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	// Prepare keys
	saleIDKey := fmt.Sprintf("sale:%d:id", saleID)
	stockKey := fmt.Sprintf("sale:%d:stock", saleID)
	itemsSoldKey := fmt.Sprintf("sale:%d:items_sold", saleID)
	startedAtKey := fmt.Sprintf("sale:%d:started_at", saleID)
	activeSaleKey := "sale:current:active_sale"

	keys := []interface{}{saleIDKey, stockKey, itemsSoldKey, startedAtKey, activeSaleKey}
	args := []interface{}{saleID, initialStock, time.Now().Unix()}

	// Execute Lua script
	result, err := redis.String(conn.Do("EVAL", AtomicInitializeSaleScript, 5, keys[0], keys[1], keys[2], keys[3], keys[4], args[0], args[1], args[2]))
	if err != nil {
		logger.Error("redis atomic initialize | failed to execute script", "error", err)
		return err
	}

	if result != "OK" {
		logger.Error("redis atomic initialize | unexpected result", "result", result)
		return fmt.Errorf("unexpected result: %s", result)
	}

	// Clear cache after new sale initialization
	r.cacheMutex.Lock()
	r.currentSaleID = saleID
	r.cachedSaleTime = time.Now()
	r.cacheMutex.Unlock()

	logger.Info("redis atomic initialize | initialized new sale", "sale_id", saleID, "initial_stock", initialStock)
	return nil
}

// GetAndDeleteCheckoutCodeAtomically gets the checkout code and deletes it atomically
func (r *RedisClient) GetAndDeleteCheckoutCodeAtomically(ctx context.Context, code string) (string, error) {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	// Step 1 - Watch the checkout code
	_, err := conn.Do("WATCH", "checkout:"+code)
	if err != nil {
		logger.Error("redis get and delete | failed to watch checkout code", "error", err)
		return "", err
	}

	// Step 2 - Get the data
	data, err := redis.String(conn.Do("GET", "checkout:"+code))
	if err == redis.ErrNil {
		logger.Debug("redis get and delete | checkout code not found", "code", code)
		return "", nil
	}
	if err != nil {
		logger.Error("redis get and delete | failed to get checkout code", "error", err)
		return "", err
	}

	// Step 3 - Start MULTI
	err = conn.Send("MULTI")
	if err != nil {
		logger.Error("redis get and delete | failed to start MULTI", "error", err)
		return "", err
	}

	// Step 4 - Queue delete
	err = conn.Send("DEL", "checkout:"+code)
	if err != nil {
		logger.Error("redis get and delete | failed to queue delete", "error", err)
		return "", err
	}

	// Step 5 - Execute
	reply, err := conn.Do("EXEC")
	if err != nil {
		logger.Error("redis get and delete | failed to execute", "error", err)
		return "", err
	}

	// Step 6 - Check if transaction was successful
	if reply == nil {
		logger.Warn("redis get and delete | transaction failed - concurrent access", "code", code)
		return "", nil
	}

	// Step 7 - Return the data
	logger.Debug("redis get and delete | successfully retrieved and deleted checkout code", "code", code)
	return data, nil
}

// UpdateActiveSalePointer updates the active sale pointer
func (r *RedisClient) UpdateActiveSalePointer(ctx context.Context, newSaleID int) error {
	logger := myLogger.FromContext(ctx, "redis")

	conn := r.pool.Get()
	defer conn.Close()

	_, err := conn.Do("SET", "sale:current:active_sale", newSaleID)
	if err != nil {
		return err
	}

	logger.Info("redis update | updated active sale pointer", "sale_id", newSaleID)
	return nil
}
