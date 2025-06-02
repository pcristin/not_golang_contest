package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// NewPostgresClient creates a new Postgres client
func NewPostgresClient(ctx context.Context, url string) (*PostgresClient, error) {
	// Open a connection to the Postgres database
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}

	// Configure the connection pool
	db.SetMaxIdleConns(25)                 // Max idle connections
	db.SetMaxOpenConns(100)                // Max open connections
	db.SetConnMaxLifetime(5 * time.Minute) // Max connection lifetime

	// Immediately test the connection
	if err := db.Ping(); err != nil {
		return nil, err
	}

	return &PostgresClient{db: db}, nil
}

// Close closes the Postgres client
func (c *PostgresClient) Close() error {
	return c.db.Close()
}

// HealthCheck checks if the Postgres client is healthy
func (c *PostgresClient) HealthCheck() error {
	return c.db.Ping()
}

// CreateTables creates the tables for the Postgres client
func (c *PostgresClient) CreateTables() error {
	// Schema
	schema := `
    CREATE TABLE IF NOT EXISTS sales (
        id SERIAL PRIMARY KEY,
        item_name VARCHAR(255) NOT NULL,
        image_url VARCHAR(500) NOT NULL,
        started_at TIMESTAMP NOT NULL,
        ended_at TIMESTAMP
    );
    
    CREATE TABLE IF NOT EXISTS checkout_attempts (
        id SERIAL PRIMARY KEY,
        user_id VARCHAR(50) NOT NULL,
        sale_id INTEGER REFERENCES sales(id),
		item_id VARCHAR(50) NOT NULL,
        code VARCHAR(32),
        status VARCHAR(30) NOT NULL,
        created_at TIMESTAMP DEFAULT NOW()
    );
    
    CREATE INDEX IF NOT EXISTS idx_code ON checkout_attempts(code) WHERE code IS NOT NULL;
    
    CREATE TABLE IF NOT EXISTS purchases (
        id SERIAL PRIMARY KEY,
        user_id VARCHAR(50) NOT NULL,
        sale_id INTEGER REFERENCES sales(id),
		item_id VARCHAR(50) NOT NULL,
        purchased_at TIMESTAMP DEFAULT NOW()
    );
    
    CREATE INDEX IF NOT EXISTS idx_user_sale ON purchases(user_id, sale_id);
    CREATE INDEX IF NOT EXISTS idx_user_item ON purchases(user_id, item_id);
    `

	// Execute the schema
	_, err := c.db.Exec(schema)
	if err != nil {
		return err
	}

	return nil
}

// InsertSale inserts a new sale into the database
func (c *PostgresClient) InsertSale(itemName, imageURL string) (int, error) {
	var saleID int
	// Insert the sale into the database
	err := c.db.QueryRow("INSERT INTO sales (item_name, image_url, started_at) VALUES ($1, $2, $3) RETURNING id",
		itemName, imageURL, time.Now()).Scan(&saleID)
	if err != nil {
		return 0, err
	}

	return saleID, nil
}

// BatchInsertAttempts inserts a batch of checkout attempts into the database
func (c *PostgresClient) BatchInsertAttempts(attempts []CheckoutAttempt) error {
	// Start a transaction
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	// Rollback the transaction if an error occurs. For success, it will be no-op
	defer tx.Rollback()

	// Prepare the statement for better perfomance
	stmt, err := tx.Prepare(`
		INSERT INTO checkout_attempts (user_id, sale_id, item_id, code, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Insert each attempt
	for _, attempt := range attempts {
		_, err := stmt.Exec(attempt.UserID, attempt.SaleID, attempt.ItemID, attempt.Code, attempt.Status, attempt.CreatedAt)
		if err != nil {
			// For now, fail the whole batch
			// Decide the best way to handle individual errors later
			return err
		}
	}
	return tx.Commit()
}

// InsertSingleAttempt inserts a single checkout attempt into the database (FALLBACK SCENARIO)
func (c *PostgresClient) InsertSingleAttempt(attempt CheckoutAttempt) error {
	_, err := c.db.Exec("INSERT INTO checkout_attempts (user_id, sale_id, item_id, code, status, created_at) VALUES ($1, $2, $3, $4, $5, $6)",
		attempt.UserID, attempt.SaleID, attempt.ItemID, attempt.Code, attempt.Status, attempt.CreatedAt)
	if err != nil {
		return err
	}
	return nil
}

// InsertPurchase inserts a purchase into the database
func (c *PostgresClient) InsertPurchase(userID string, saleID int, itemID string) error {
	_, err := c.db.Exec("INSERT INTO purchases (user_id, sale_id, item_id, purchased_at) VALUES ($1, $2, $3, $4)",
		userID, saleID, itemID, time.Now())
	if err != nil {
		return err
	}
	return nil
}

// GetCheckoutAttemptByCode gets the checkout attempt for a user by code
func (c *PostgresClient) GetCheckoutAttemptByCode(code string) (*CheckoutAttempt, error) {
	var attempt CheckoutAttempt
	err := c.db.QueryRow("SELECT id, user_id, sale_id, item_id, code, status, created_at FROM checkout_attempts WHERE code = $1", code).Scan(
		&attempt.ID,
		&attempt.UserID,
		&attempt.SaleID,
		&attempt.ItemID,
		&attempt.Code,
		&attempt.Status,
		&attempt.CreatedAt)
	if err != nil {
		return nil, err
	} else if err == sql.ErrNoRows {
		return nil, nil
	}
	return &attempt, nil
}

// CompletePurchase completes a purchase in a transaction
func (c *PostgresClient) CompletePurchase(code string, userID string, saleID int, itemID string) error {
	// Start a transaction
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	// Rollback the transaction if an error occurs. For success, it will be no-op
	defer tx.Rollback()

	// Get attempt ID and verify it's still pending for purchase
	var attemptID int
	var status string
	err = tx.QueryRow("SELECT id, status FROM checkout_attempts WHERE code = $1 FOR UPDATE",
		code,
	).Scan(&attemptID, &status)
	if err != nil {
		return err
	} else if err == sql.ErrNoRows {
		return fmt.Errorf("checkout attempt not found or already completed")
	} else if status != "success" {
		return fmt.Errorf("checkout attempt already completed")
	}

	// Update the attempt status to completed
	_, err = tx.Exec("UPDATE checkout_attempts SET status = 'completed' WHERE id = $1", attemptID)
	if err != nil {
		return err
	}

	// Insert the purchase
	_, err = tx.Exec("INSERT INTO purchases (user_id, sale_id, item_id, purchased_at) VALUES ($1, $2, $3, $4)",
		userID, saleID, itemID, time.Now())
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetSaleByID gets a sale by ID
func (c *PostgresClient) GetSaleByID(saleID int) (string, string, error) {
	var itemName, imageURL string
	err := c.db.QueryRow("SELECT item_name, image_url FROM sales WHERE id = $1", saleID).Scan(
		&itemName,
		&imageURL,
	)
	if err != nil {
		return "", "", err
	}
	return itemName, imageURL, nil
}

// GetExpiredCheckoutAttempts gets all checkout attempts that are expired
func (c *PostgresClient) GetExpiredCheckoutAttempts(expiredAfter time.Duration) ([]CheckoutAttempt, error) {
	stmt, err := c.db.Prepare(`
		SELECT id, user_id, sale_id, item_id, code, status, created_at 
		FROM checkout_attempts 
		WHERE status = 'success' 
		AND created_at < $1
		ORDER BY created_at
		LIMIT 100
	`)

	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	cutoff := time.Now().Add(-expiredAfter)

	rows, err := stmt.Query(cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []CheckoutAttempt
	for rows.Next() {
		var attempt CheckoutAttempt
		err := rows.Scan(&attempt.ID, &attempt.UserID, &attempt.SaleID, &attempt.ItemID, &attempt.Code, &attempt.Status, &attempt.CreatedAt)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, nil
}

// MarkAttemptsExpired marks all checkout attempts that are expired as expired
func (c *PostgresClient) MarkAttemptsExpired(attemptsIDs []int) error {
	if len(attemptsIDs) == 0 {
		return nil
	}

	// Build placeholders for the IN clause
	placeholders := make([]string, len(attemptsIDs))
	args := make([]interface{}, len(attemptsIDs))
	for i, attemptID := range attemptsIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = attemptID
	}

	// Build the query
	query := fmt.Sprintf("UPDATE checkout_attempts SET status = 'expired' WHERE id IN (%s)",
		strings.Join(placeholders, ", "))

	// Start a transaction
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Execute the query
	_, err = tx.Exec(query, args...)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetLastSaleStartTime gets the start time of the last sale
func (c *PostgresClient) GetLastSaleStartTime() (time.Time, error) {
	var startTime time.Time
	err := c.db.QueryRow("SELECT started_at FROM sales ORDER BY started_at DESC LIMIT 1").Scan(&startTime)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	} else if err != nil {
		return time.Time{}, err
	}
	return startTime, nil
}

// GetActiveSaleID gets the ID of the active sale
func (c *PostgresClient) GetActiveSaleID() (int, error) {
	var saleID int
	err := c.db.QueryRow("SELECT id FROM sales WHERE ended_at IS NULL ORDER BY id DESC LIMIT 1").Scan(&saleID)
	if err == sql.ErrNoRows {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	return saleID, nil
}

// EndSale ends the active sale (mark it as ended)
func (c *PostgresClient) EndSale(saleID int) error {
	_, err := c.db.Exec("UPDATE sales SET ended_at = $1 WHERE id = $2", time.Now(), saleID)
	return err
}

// BatchInsertPurchases inserts a batch of purchases into the database
func (c *PostgresClient) BatchInsertPurchases(purchases []Purchase) error {
	// Start a transaction
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	// Rollback the transaction if an error occurs. For success, it will be no-op
	defer tx.Rollback()

	// Prepare the statement for better perfomance
	stmt, err := tx.Prepare(`
		INSERT INTO purchases (user_id, sale_id, item_id, purchased_at)
		VALUES ($1, $2, $3, $4)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Insert each purchase
	for _, purchase := range purchases {
		_, err := stmt.Exec(purchase.UserID, purchase.SaleID, purchase.ItemID, purchase.PurchasedAt)
		if err != nil {
			// For now, fail the whole batch
			// Decide the best way to handle individual errors later
			return err
		}
	}
	return tx.Commit()
}
