package broker

import (
	"log"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

type JetStreamBroker struct {
	Conn *nats.Conn
	JS   nats.JetStreamContext
}

func NewBrokerWithConn(nc *nats.Conn) (*JetStreamBroker, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, err
	}

	// Create or update the stream JOBS
	streamName := "JOBS"
	subjects := []string{
		"jobs.build.*",      // e.g. jobs.build.llama3
		"jobs.results",      // worker publishes results here
		"workers.register",  // worker publishes registration details here
		"workers.heartbeat", // worker publishes heartbeat pings here
	}

	// Check stream info
	_, err = js.StreamInfo(streamName)
	if err != nil {
		// Create stream if not exists
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     streamName,
			Subjects: subjects,
		})
		if err != nil {
			return nil, err
		}
		log.Printf("📥 JetStream Stream '%s' created successfully.", streamName)
	} else {
		// Update stream config if it already exists
		_, err = js.UpdateStream(&nats.StreamConfig{
			Name:     streamName,
			Subjects: subjects,
		})
		if err != nil {
			log.Printf("⚠️ Warning: Could not update JetStream stream configuration: %v", err)
		} else {
			log.Printf("📥 JetStream Stream '%s' verified/updated.", streamName)
		}
	}

	return &JetStreamBroker{
		Conn: nc,
		JS:   js,
	}, nil
}

func (b *JetStreamBroker) Close() {
	// Connection lifecycle managed externally (P2P Agent)
}

// PublishJob publishes a JobRequest to NATS JetStream under the subject jobs.build.<modelName>
func (b *JetStreamBroker) PublishJob(modelName string, req *workerpb.JobRequest) error {
	data, err := proto.Marshal(req)
	if err != nil {
		return err
	}

	subject := "jobs.build." + modelName
	_, err = b.JS.Publish(subject, data)
	if err != nil {
		return err
	}

	log.Printf("📤 Published Job %s to subject %s", req.JobId, subject)
	return nil
}
