package workerman

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

func (wm *WorkerManager) RegisterWorker(ctx context.Context, info *workerpb.WorkerInfo) error {
	worker := &Worker{
		Info:           info,
		LastSeen:       time.Now(),
		State:          WorkerFree,
		SupportedModel: info.SupportedModel,
	}

	data, err := json.Marshal(worker)
	if err != nil {
		return fmt.Errorf("failed to marshal worker: %w", err)
	}

	_, err = wm.kv.Put(info.Id, data)
	if err != nil {
		return fmt.Errorf("failed to write worker to KV store: %w", err)
	}

	log.Printf("registered worker %s with models %v", info.Id, info.SupportedModel)
	return nil
}
