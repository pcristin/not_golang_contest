package database

// Lua scripts for atomic operations
const (
	// AtomicCheckoutScript performs all checkout validations and counter updates atomically
	// KEYS: [1] stock_key, [2] user_count_key, [3] items_sold_key
	// ARGV: [1] user_id, [2] max_items_per_user, [3] max_total_items
	// Returns: [stock_remaining, user_count, items_sold, status_code]
	// Status codes: 0=success, 1=out_of_stock, 2=user_limit_exceeded, 3=sale_limit_exceeded
	AtomicCheckoutScript = `
		local stock_key = KEYS[1]
		local user_count_key = KEYS[2] 
		local items_sold_key = KEYS[3]
		
		local user_id = ARGV[1]
		local max_user_items = tonumber(ARGV[2])
		local max_total_items = tonumber(ARGV[3])
		
		-- Get current values
		local current_stock = tonumber(redis.call('GET', stock_key) or 0)
		local current_user_count = tonumber(redis.call('GET', user_count_key) or 0)
		local current_items_sold = tonumber(redis.call('GET', items_sold_key) or 0)
		
		-- Check stock availability
		if current_stock <= 0 then
			return {current_stock, current_user_count, current_items_sold, 1}
		end
		
		-- Check user limit
		if current_user_count >= max_user_items then
			return {current_stock, current_user_count, current_items_sold, 2}
		end
		
		-- Check total items limit
		if current_items_sold >= max_total_items then
			return {current_stock, current_user_count, current_items_sold, 3}
		end
		
		-- All checks passed - perform atomic updates
		local new_stock = redis.call('DECR', stock_key)
		local new_user_count = redis.call('INCR', user_count_key)
		local new_items_sold = redis.call('INCR', items_sold_key)
		
		return {new_stock, new_user_count, new_items_sold, 0}
	`

	// AtomicRollbackScript rolls back a failed checkout atomically
	// KEYS: [1] stock_key, [2] user_count_key, [3] items_sold_key
	// ARGV: [1] user_id
	// Returns: [new_stock, new_user_count, new_items_sold]
	AtomicRollbackScript = `
		local stock_key = KEYS[1]
		local user_count_key = KEYS[2]
		local items_sold_key = KEYS[3]
		
		-- Rollback operations
		local new_stock = redis.call('INCR', stock_key)
		local new_user_count = redis.call('DECR', user_count_key)
		local new_items_sold = redis.call('DECR', items_sold_key)
		
		-- Ensure counters don't go below 0
		if new_user_count < 0 then
			redis.call('SET', user_count_key, 0)
			new_user_count = 0
		end
		
		if new_items_sold < 0 then
			redis.call('SET', items_sold_key, 0)
			new_items_sold = 0
		end
		
		return {new_stock, new_user_count, new_items_sold}
	`

	// AtomicCleanupExpiredCheckoutScript cleans up expired checkout and updates counters
	// KEYS: [1] stock_key, [2] user_count_key, [3] items_sold_key
	// ARGV: [1] user_id
	// Returns: [new_stock, new_user_count, new_items_sold]
	AtomicCleanupExpiredCheckoutScript = `
		local stock_key = KEYS[1]
		local user_count_key = KEYS[2]
		local items_sold_key = KEYS[3]
		
		-- Cleanup expired checkout - restore counters
		local new_stock = redis.call('INCR', stock_key)
		local new_user_count = redis.call('DECR', user_count_key)
		local new_items_sold = redis.call('DECR', items_sold_key)
		
		-- Ensure counters don't go below 0
		if new_user_count < 0 then
			redis.call('SET', user_count_key, 0)
			new_user_count = 0
		end
		
		if new_items_sold < 0 then
			redis.call('SET', items_sold_key, 0)
			new_items_sold = 0
		end
		
		return {new_stock, new_user_count, new_items_sold}
	`

	// AtomicInitializeSaleScript initializes all counters for a new sale
	// KEYS: [1] sale_id_key, [2] stock_key, [3] items_sold_key, [4] started_at_key, [5] active_sale_key
	// ARGV: [1] sale_id, [2] initial_stock, [3] current_timestamp
	// Returns: "OK" on success
	AtomicInitializeSaleScript = `
		local sale_id_key = KEYS[1]
		local stock_key = KEYS[2]
		local items_sold_key = KEYS[3]
		local started_at_key = KEYS[4]
		local active_sale_key = KEYS[5]
		
		local sale_id = ARGV[1]
		local initial_stock = tonumber(ARGV[2])
		local timestamp = ARGV[3]
		
		-- Set all sale data atomically with TTL (1 hour)
		redis.call('SETEX', sale_id_key, 3600, sale_id)
		redis.call('SETEX', stock_key, 3600, initial_stock)
		redis.call('SETEX', items_sold_key, 3600, 0)
		redis.call('SETEX', started_at_key, 3600, timestamp)
		redis.call('SET', active_sale_key, sale_id)
		
		return "OK"
	`
)

// CheckoutResult represents the result of an atomic checkout operation
type CheckoutResult struct {
	StockRemaining int64
	UserCount      int64
	ItemsSold      int64
	Status         CheckoutStatus
}

// CheckoutStatus represents the status of a checkout operation
type CheckoutStatus int

const (
	CheckoutSuccess CheckoutStatus = iota
	CheckoutOutOfStock
	CheckoutUserLimitExceeded
	CheckoutSaleLimitExceeded
)

func (s CheckoutStatus) String() string {
	switch s {
	case CheckoutSuccess:
		return "success"
	case CheckoutOutOfStock:
		return "out_of_stock"
	case CheckoutUserLimitExceeded:
		return "user_limit_exceeded"
	case CheckoutSaleLimitExceeded:
		return "sale_limit_exceeded"
	default:
		return "unknown"
	}
}

func (s CheckoutStatus) IsSuccess() bool {
	return s == CheckoutSuccess
}
