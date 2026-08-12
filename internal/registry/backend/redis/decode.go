package redis

import (
	"fmt"
	"strconv"
	"time"
)

func decodeUnixMilli(value string) (time.Time, error) {
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid unix milliseconds %q: %w", value, err)
	}
	return time.UnixMilli(milliseconds), nil
}
