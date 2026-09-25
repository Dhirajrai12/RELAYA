package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Listen LISTENs on channel and sends to wake (without blocking) for every
// notification, reconnecting on errors until ctx is done. Background workers
// use it to react within milliseconds instead of waiting for their next poll.
func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, wake chan<- struct{}) {
	for ctx.Err() == nil {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			sleep(ctx, time.Second)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+channel); err == nil {
			for {
				if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
					break
				}
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
		// A connection that may still be LISTENing must not go back to the pool.
		conn.Hijack().Close(context.Background())
		sleep(ctx, time.Second)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
