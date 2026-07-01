package broker

import (
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

const (
	StreamName = "JOBS"

	// Training job subjects
	SubjectTrainPrefix   = "jobs.train."
	SubjectTrainWildcard = "jobs.train.*"
	SubjectResults       = "jobs.results"
	SubjectProgress      = "jobs.progress"
	SubjectCancel        = "jobs.cancel"
	SubjectLogsPrefix    = "jobs.logs."
	SubjectLogsWildcard  = "jobs.logs.*"

	// Worker lifecycle subjects
	SubjectRegister  = "workers.register"
	SubjectHeartbeat = "workers.heartbeat"

	// Object Store bucket names
	BucketDatasets    = "datasets"
	BucketCheckpoints = "checkpoints"
)

// Broker wraps the shared NATS JetStream client.
type Broker struct {
	Conn     *nats.Conn
	JS       nats.JetStreamContext
	Replicas int // number of NATS nodes that store each stream/KV/object replica
}

// NewBroker creates a broker from an existing NATS connection.
// replicas controls the JetStream replication factor: 1 for single-node dev,
// 3 for a production cluster (tolerates 1 replica node failure).
func NewBroker(nc *nats.Conn, replicas int) (*Broker, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}
	if replicas < 1 {
		replicas = 1
	}
	return &Broker{Conn: nc, JS: js, Replicas: replicas}, nil
}

// EnsureStream creates or updates the JOBS stream covering all subjects.
func (b *Broker) EnsureStream() error {
	subjects := []string{
		SubjectTrainWildcard,
		SubjectResults,
		SubjectProgress,
		SubjectCancel,
		SubjectLogsWildcard,
		SubjectRegister,
		SubjectHeartbeat,
	}

	_, err := b.JS.StreamInfo(StreamName)
	if err != nil {
		_, err = b.JS.AddStream(&nats.StreamConfig{
			Name:     StreamName,
			Subjects: subjects,
			Replicas: b.Replicas,
		})
		if err != nil {
			return fmt.Errorf("failed to add stream: %w", err)
		}
		log.Printf("JetStream stream %q created (replicas=%d)", StreamName, b.Replicas)
	} else {
		_, err = b.JS.UpdateStream(&nats.StreamConfig{
			Name:     StreamName,
			Subjects: subjects,
			Replicas: b.Replicas,
		})
		if err != nil {
			log.Printf("could not update JetStream stream configuration: %v", err)
		} else {
			log.Printf("JetStream stream %q verified", StreamName)
		}
	}
	return nil
}

// PublishProto serializes a protobuf message and publishes it to a NATS subject.
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
