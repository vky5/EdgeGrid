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
	SubjectCancel        = "jobs.cancel"
	SubjectLogsPrefix    = "jobs.logs."
	SubjectLogsWildcard  = "jobs.logs.*"

	// Worker lifecycle subjects
	SubjectRegister  = "workers.register"
	SubjectHeartbeat = "workers.heartbeat"

	// Worker approval subjects (NATS Core — not JetStream, ephemeral signals)
	SubjectWorkerReject      = "workers.reject"
	SubjectWorkerDecisionFmt = "workers.decision.%s.%s" // workerID, jobID

	// Live resource usage published at each heartbeat (NATS Core, not JetStream)
	SubjectWorkerStatsWildcard = "workers.stats.*"
	SubjectWorkerStatsFmt      = "workers.stats.%s" // workerID

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
// JOBS is a single event log with subject names as the addrss for each msg
/*
Stream: JOBS

sequence  subject                 payload
1         workers.register         worker-a info
2         jobs.train.worker-a      train job 1
3         jobs.logs.job-1          "downloading model..."
4         workers.heartbeat        worker-a alive
5         jobs.results             job 1 completed
6         jobs.train.worker-b      train job 2

jetstream is not a queue and cosumer is like a pointer for a subject on this event log
*/
func (b *Broker) EnsureStream() error {
	subjects := []string{
		SubjectTrainWildcard,
		SubjectResults,
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
