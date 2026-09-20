package blob

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// A receiver answers every manifest with a verdict before any chunk moves:
//
//	[1 byte: 0 accept, 1 refuse][2-byte reason length][reason bytes]
//
// Without it, refusing meant hanging up, which a sender cannot tell apart
// from a crash or a dropped network — it would just see a broken pipe partway
// through a large transfer, or block until its deadline.
const (
	verdictAccept byte = 0
	verdictRefuse byte = 1
)

// maxReasonLen bounds a refusal reason so a hostile peer can't make us read
// an arbitrary amount of text into an error message.
const maxReasonLen = 1024

// RefusedError is what a sender gets back when the receiver declines.
type RefusedError struct{
	Reason string 
}

func (e *RefusedError) Error() string { // defining error so it can be used direclty for error
	if e.Reason == "" {
		return "peer refused the transfer"
	}
	return "peer refused the transfer: " + e.Reason
}

// WriteVerdict sends the receiver's answer. A nil refusal accepts; otherwise
// the error's text goes to the sender as the reason, so it must be safe to
// show them.
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

// ReadVerdict reads the receiver's answer: nil means go ahead, a
// *RefusedError means it declined, anything else is a transport failure.
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
