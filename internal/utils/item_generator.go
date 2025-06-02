package utils

import (
	"fmt"
	"time"
)

func GenerateItem(saleID int, startTime time.Time) (itemName, imageURL string) {
	// Generate a random item name
	itemName = fmt.Sprintf("NOT-DEVELOPER-ITEM-%d", saleID)

	// Generate a random image URL
	imageURL = fmt.Sprintf("https://via.placeholder.com/150?text=%s", itemName)

	return itemName, imageURL
}
