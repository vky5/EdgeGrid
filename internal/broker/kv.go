package broker

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// GetOrCreateKV creates or retrieves a NATS Key-Value bucket.
// TTL applies to individual keys — NATS auto-deletes them after the duration.
func (b *Broker) GetOrCreateKV(bucket string, ttl time.Duration) (nats.KeyValue, error) {
	kv, err := b.JS.KeyValue(bucket)
	if err != nil {
		kv, err = b.JS.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:   bucket,
			TTL:      ttl,
			Replicas: b.Replicas,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create KV bucket %s: %w", bucket, err)
		}
	}
	return kv, nil
}
