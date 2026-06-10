package workerman

import (
	"context"
	"log"
	"time"
)

func (wm *WorkerManager) StartHealthChecker(ctx context.Context, interval time.Duration) {
	log.Printf("Distributed health checking: relying on NATS KV TTL auto-reaping (TTL: 1m)")
}
