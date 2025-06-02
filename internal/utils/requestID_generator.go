package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func GenerateRequestID() string {
	timestamp := time.Now().UnixNano()
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	return fmt.Sprintf("%d-%s", timestamp, hex.EncodeToString(randBytes))
}
