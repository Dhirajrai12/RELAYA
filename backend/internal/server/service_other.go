//go:build !windows

package server

import "context"

func asService(ctx context.Context, stop context.CancelFunc) (context.Context, context.CancelFunc) {
	return ctx, stop
}
