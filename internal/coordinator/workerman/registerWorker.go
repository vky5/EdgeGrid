// actually we need this func for the gRPC only but we are gonna separate this incase we need to call this logic from somewhere elsee

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

	log.Printf("✅ New Worker registered with ID: %s, Supported Models: %v", info.Id, info.SupportedModel)

	return nil
}

