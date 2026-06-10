package broker

import (
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

const (
	StreamName          = "JOBS"
	SubjectJobsPrefix   = "jobs.build."
	SubjectJobsWildcard = "jobs.build.*"
	SubjectResults      = "jobs.results"
	SubjectRegister     = "workers.register"
	SubjectHeartbeat    = "workers.heartbeat"
)

// Broker wraps the shared NATS JetStream client.
type Broker struct {
	Conn *nats.Conn
	JS   nats.JetStreamContext
}

// NewBroker creates a broker from an existing NATS connection.
func NewBroker(nc *nats.Conn) (*Broker, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	return &Broker{
		Conn: nc,
		JS:   js,
	}, nil
}

// EnsureStream creates or updates the JOBS stream.
func (b *Broker) EnsureStream() error {
	subjects := []string{
		SubjectJobsWildcard,
		SubjectResults,
		SubjectRegister,
		SubjectHeartbeat,
	}

	_, err := b.JS.StreamInfo(StreamName)
	if err != nil {
		_, err = b.JS.AddStream(&nats.StreamConfig{
			Name:     StreamName,
			Subjects: subjects,
		})
		if err != nil {
			return fmt.Errorf("failed to add stream: %w", err)
		}
		log.Printf("JetStream stream %q created", StreamName)
	} else {
		_, err = b.JS.UpdateStream(&nats.StreamConfig{
			Name:     StreamName,
			Subjects: subjects,
		})
		if err != nil {
			log.Printf("could not update JetStream stream configuration: %v", err)
		} else {
			log.Printf("JetStream stream %q verified", StreamName)
		}
	}
	return nil
}

// PublishProto publishes a protobuf message to a NATS subject.
func (b *Broker) PublishProto(subject string, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal proto message: %w", err)
	}

	_, err = b.JS.Publish(subject, data)
	if err != nil {
		return fmt.Errorf("failed to publish to subject %s: %w", subject, err)
	}
	return nil
}

// GetOrCreateKV creates or retrieves a Key-Value bucket
func (b *Broker) GetOrCreateKV(
	bucket string, 
	ttl time.Duration, // for the individual key in the bucket not entire bucket
) (nats.KeyValue, error) {
	kv, err := b.JS.KeyValue(bucket)
	if err != nil {
		// Bucket might not exist, create it
		kv, err = b.JS.CreateKeyValue(&nats.KeyValueConfig{
			Bucket: bucket,
			TTL:    ttl,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create KV bucket %s: %w", bucket, err)
		}
	}
	return kv, nil
}
