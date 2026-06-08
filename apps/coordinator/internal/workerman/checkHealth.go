// this will handle checking the health of the workers.
// Currently stubbed out as we move to a passive heartbeat model.

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
			log.Println("Health checker stopped.")
			return
		case <-ticker.C:
			// Stubbed out for now.
		}
	}
}

