package task

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func pipe(t *testing.T) (client, server net.Conn) {
	t.Helper()
	client, server = net.Pipe()
	deadline := time.Now().Add(5 * time.Second)
	client.SetDeadline(deadline)
	server.SetDeadline(deadline)
	t.Cleanup(func() { client.Close(); server.Close() })
	return
}

func TestResultRoundTrip(t *testing.T) {
	client, server := pipe(t)
	id := uuid.NewString()
	want := ResultResponse{Status: StatusDone, Output: []byte("hello\n\x00bin")}

	errc := make(chan error, 1)
	go func() {
		errc <- ServeResult(server, func(got string) ResultResponse {
			if got != id {
				t.Errorf("lookup got %q, want %q", got, id)
			}
			return want
		})
	}()

	resp, err := AskResult(client, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusDone || !bytes.Equal(resp.Output, want.Output) {
		t.Errorf("resp = %+v, want %+v", resp, want)
	}
}

func TestMaxOutputFitsInAFrame(t *testing.T) {
	client, server := pipe(t)
	out := bytes.Repeat([]byte{0xff}, 64<<10) // worst case for base64 size
	go ServeResult(server, func(string) ResultResponse {
		return ResultResponse{Status: StatusDone, Output: out}
	})
	resp, err := AskResult(client, uuid.NewString())
	if err != nil || len(resp.Output) != len(out) {
		t.Fatalf("len=%d err=%v, want a full 64KB output through", len(resp.Output), err)
	}
}

func TestServeResultAnswersUnknownForABadIDWithoutLookup(t *testing.T) {
	client, server := pipe(t)
	go ServeResult(server, func(string) ResultResponse {
		t.Error("lookup called for a malformed id")
		return ResultResponse{}
	})
	resp, err := AskResult(client, "not-a-uuid")
	if err != nil || resp.Status != StatusUnknown {
		t.Errorf("resp = %+v, err = %v, want unknown", resp, err)
	}
}

func TestReadResultFrameRejectsBadFrames(t *testing.T) {
	cases := map[string][]byte{
		"wrong version": {9, 0, 0, 0, 2, '{', '}'},
		"oversized":     {resultWireVersion, 0xff, 0xff, 0xff, 0xff},
		"truncated":     {resultWireVersion, 0, 0, 0, 10, '{'},
		"garbled json":  {resultWireVersion, 0, 0, 0, 3, 'n', 'o', 'p'},
		"empty":         {},
	}
	for name, raw := range cases {
		var resp ResultResponse
		if err := readResultFrame(bytes.NewReader(raw), &resp); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	err := readResultFrame(bytes.NewReader(cases["wrong version"]), &ResultResponse{})
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("wrong version err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestAskResultRejectsAnUnknownStatus(t *testing.T) {
	client, server := pipe(t)
	go func() {
		var req ResultRequest
		readResultFrame(server, &req)
		writeResultFrame(server, ResultResponse{Status: "banana"})
	}()
	if _, err := AskResult(client, uuid.NewString()); err == nil || !strings.Contains(err.Error(), "banana") {
		t.Errorf("err = %v, want the bad status named", err)
	}
}

func TestFinished(t *testing.T) {
	if StatusRunning.Finished() {
		t.Error("running counted as finished")
	}
	for _, s := range []ResultStatus{StatusDone, StatusFailed, StatusInterrupted, StatusUnknown} {
		if !s.Finished() {
			t.Errorf("%s not finished", s)
		}
	}
}
