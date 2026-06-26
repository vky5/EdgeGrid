package broker

import (
	"fmt"
	"io"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	ttlDatasets    = 48 * time.Hour
	ttlCheckpoints = 7 * 24 * time.Hour
)

// GetOrCreateObjectStore creates or retrieves a NATS Object Store bucket.
func (b *Broker) GetOrCreateObjectStore(bucket string, ttl time.Duration) (nats.ObjectStore, error) {
	obs, err := b.JS.ObjectStore(bucket)
	if err != nil {
		obs, err = b.JS.CreateObjectStore(&nats.ObjectStoreConfig{
			Bucket:   bucket,
			TTL:      ttl,
			Storage:  nats.FileStorage,
			Replicas: b.Replicas,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create object store bucket %s: %w", bucket, err)
		}
	}
	return obs, nil
}

// PushDataset uploads a dataset file for a job (48h TTL).
func (b *Broker) PushDataset(jobID string, r io.Reader) error {
	obs, err := b.GetOrCreateObjectStore(BucketDatasets, ttlDatasets)
	if err != nil {
		return err
	}
	_, err = obs.Put(&nats.ObjectMeta{Name: jobID}, r)
	return err
}

// PullDataset retrieves the dataset for a job.
// Caller must close the returned ObjectResult after reading.
func (b *Broker) PullDataset(jobID string) (nats.ObjectResult, error) {
	obs, err := b.GetOrCreateObjectStore(BucketDatasets, ttlDatasets)
	if err != nil {
		return nil, err
	}
	return obs.Get(jobID)
}

// DatasetInfo returns size and SHA-256 metadata without downloading the dataset.
// Used by the worker for the disk pre-check before pulling.
func (b *Broker) DatasetInfo(jobID string) (*nats.ObjectInfo, error) {
	obs, err := b.GetOrCreateObjectStore(BucketDatasets, ttlDatasets)
	if err != nil {
		return nil, err
	}
	return obs.GetInfo(jobID)
}

// PushCheckpoint uploads the trained model output for a job (7-day TTL).
func (b *Broker) PushCheckpoint(jobID string, r io.Reader) error {
	obs, err := b.GetOrCreateObjectStore(BucketCheckpoints, ttlCheckpoints)
	if err != nil {
		return err
	}
	_, err = obs.Put(&nats.ObjectMeta{Name: jobID}, r)
	return err
}

// PullCheckpoint retrieves the trained checkpoint for a job.
// Caller must close the returned ObjectResult after reading.
func (b *Broker) PullCheckpoint(jobID string) (nats.ObjectResult, error) {
	obs, err := b.GetOrCreateObjectStore(BucketCheckpoints, ttlCheckpoints)
	if err != nil {
		return nil, err
	}
	return obs.Get(jobID)
}
