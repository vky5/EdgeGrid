package blob

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// the receiver's yes or no to a manifest, sent before any chunk moves:
//
//	[1 byte: 0 accept, 1 refuse][2-byte reason length][reason bytes]
//
// without it a refusal is just a hang-up, which the sender can't tell from a crash
const (
	verdictAccept byte = 0
	verdictRefuse byte = 1
)

// the longest refusal reason we'll read, 1024 bytes, so a peer can't send us endless text
const maxReasonLen = 1024

// what the sender gets back when the receiver says no
// e.g. "peer refused the transfer: this node is not accepting files from you"
type RefusedError struct {
	Reason string
}

func (e *RefusedError) Error() string { // defining error so it can be used direclty for error
	if e.Reason == "" {
		return "peer refused the transfer"
	}
	return "peer refused the transfer: " + e.Reason
}

// sends the receiver's answer: nil means yes, an error means no
// the error text goes to the sender as the reason, so it must be safe to show them
func WriteVerdict(w io.Writer, refusal error) error {
	status, reason := verdictAccept, ""
	if refusal != nil {
		status, reason = verdictRefuse, refusal.Error()
		if len(reason) > maxReasonLen {
			reason = reason[:maxReasonLen]
		}
	}

	buf := make([]byte, 3, 3+len(reason)) // [1 byte for the result] [2 byte for length] [actual length]
	buf[0] = status
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(reason)))
	buf = append(buf, reason...)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("blob: write verdict: %w", err)
	}
	return nil
}

// reads the receiver's answer: nil is yes, *RefusedError is no, any other error is a broken connection
func ReadVerdict(r io.Reader) error {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return fmt.Errorf("blob: read verdict: %w", err)
	}
	n := binary.BigEndian.Uint16(hdr[1:3])
	if n > maxReasonLen {
		return fmt.Errorf("blob: verdict reason length %d exceeds max %d", n, maxReasonLen)
	}
	reason := make([]byte, n)
	if _, err := io.ReadFull(r, reason); err != nil {
		return fmt.Errorf("blob: read verdict reason: %w", err)
	}

	switch hdr[0] {
	case verdictAccept:
		return nil
	case verdictRefuse:
		return &RefusedError{Reason: string(reason)}
	default:
		return errors.New("blob: unknown verdict status")
	}
}
