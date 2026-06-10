package workerman

import (
	"context"
	"log"
	"time"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

func (wm *WorkerManager) RegisterWorker(ctx context.Context, info *workerpb.WorkerInfo) error {
	wm.mu.Lock()
	wm.workers[info.Id] = &Worker{
		Info:           info,
		LastSeen:       time.Now(),
		State:          WorkerFree,
		SupportedModel: info.SupportedModel,
	}
	wm.mu.Unlock()

	log.Printf("registered worker %s with models %v", info.Id, info.SupportedModel)

	return nil
}
