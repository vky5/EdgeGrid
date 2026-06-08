package broker

import (
	"fmt"
	"log"

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

// Broker is a shared thin wrapper around NATS JetStream
type Broker struct {
	Conn *nats.Conn
	JS   nats.JetStreamContext
}

// NewBroker creates a new shared broker instance
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

// EnsureStream guarantees that the JOBS stream exists with proper subjects
func (b *Broker) EnsureStream() error {
	subjects := []string{
		SubjectJobsWildcard,
		SubjectResults,
		SubjectRegister,
		SubjectHeartbeat,
	}

	_, err := b.JS.StreamInfo(StreamName)
	if err != nil {
		// Create stream if not exists
		_, err = b.JS.AddStream(&nats.StreamConfig{
			Name:     StreamName,
			Subjects: subjects,
		})
		if err != nil {
			return fmt.Errorf("failed to add stream: %w", err)
		}
		log.Printf("📥 JetStream Stream '%s' created successfully.", StreamName)
	} else {
		// Update stream config if it already exists
		_, err = b.JS.UpdateStream(&nats.StreamConfig{
			Name:     StreamName,
			Subjects: subjects,
		})
		if err != nil {
			log.Printf("⚠️ Warning: Could not update JetStream stream configuration: %v", err)
		} else {
			log.Printf("📥 JetStream Stream '%s' verified/updated.", StreamName)
		}
	}
	return nil
}

// PublishProto marshals a protobuf message and publishes it to a NATS subject
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
