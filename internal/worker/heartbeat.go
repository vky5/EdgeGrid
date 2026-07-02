package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/edgegrid/edgegrid/internal/worker/hardware"
)

// WorkerStats is published at each heartbeat over NATS Core (not JetStream).
type WorkerStats struct {
	RAMUsedGB   float32 `json:"ram_used_gb"`
	DiskUsedGB  float32 `json:"disk_used_gb"`
	DiskTotalGB float32 `json:"disk_total_gb"`
}

// RegisterWorker publishes the worker's hardware capabilities detected at startup.
func (a *Worker) RegisterWorker() error {
	info := &workerpb.WorkerInfo{
		Id:         a.id,
		HasGpu:     a.hw.HasGPU,
		GpuName:    a.hw.GPUName,
		GpuVramGb:  a.hw.GPUVramGB,
		RamGb:      a.hw.RAMGB,
		DiskFreeGb: a.hw.DiskFreeGB,
		Sandbox:    "none",
	}
	return a.broker.PublishProto(broker.SubjectRegister, info)
}

// StartHeartbeat sends periodic worker status updates to the coordinator.
func (a *Worker) StartHeartbeat(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status := WorkerFree
			if a.busy.Load() {
				status = WorkerBusy
			}
			req := &workerpb.PingRequest{
				Id:     a.id,
				Status: status,
			}
			if err := a.broker.PublishProto(broker.SubjectHeartbeat, req); err != nil {
				log.Printf("failed to publish heartbeat: %v", err)
			}

			// Publish live resource usage on a separate NATS Core subject so
			// the coordinator can update the dashboard without proto changes.
			stats := WorkerStats{
				RAMUsedGB:   hardware.LiveRAMUsedGB(),
				DiskUsedGB:  hardware.LiveDiskUsedGB(),
				DiskTotalGB: hardware.LiveDiskTotalGB(),
			}
			if data, err := json.Marshal(stats); err == nil {
				subject := fmt.Sprintf(broker.SubjectWorkerStatsFmt, a.id)
				_ = a.broker.Conn.Publish(subject, data)
			}
		}
	}
}
