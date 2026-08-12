package redis

import (
	"context"
	"errors"

	redisclient "github.com/redis/go-redis/v9"
)

func (b *Backend) activeIndexIDs(ctx context.Context, key string) ([]string, error) {
	ids, err := activeIndexScript.Run(ctx, b.client, []string{key}).StringSlice()
	if errors.Is(err, redisclient.Nil) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ids, nil
}
