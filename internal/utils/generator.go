package utils

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"sync/atomic"
	"time"
)

var counter int64

// GenerateCode function generates a random code for checkout handler
// without external dependencies
func GenerateCode() string {
	timestamp := time.Now().UnixMicro()

	count := atomic.AddInt64(&counter, 1)

	random := make([]byte, 8)
	rand.Read(random)

	combined := fmt.Sprintf("%d-%d-%s", timestamp, count, random)

	return base32.StdEncoding.EncodeToString([]byte(combined))[:16]
}
