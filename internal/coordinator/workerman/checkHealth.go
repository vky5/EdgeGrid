package workerman

import (
	"context"
	"log"
	"time"
)

func (wm *WorkerManager) StartHealthChecker(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("health checker stopped")
			return
		case <-ticker.C:
		}
	}
}
