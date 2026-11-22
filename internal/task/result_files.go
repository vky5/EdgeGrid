package task

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/edgegrid/edgegrid/internal/blob"
)

// one file a done task left for the sender, as listed in the result answer
//
//	{"name":"model.pt","size":10485760,"sha256":"9f2c41…"}
type ResultFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// the sender's "ready, send these", right after an answer that listed files
// any subset of the list, in the order wanted; empty means none
//
//	{"want":["model.pt","metrics.json","confusion.png"]}   // 3 of the 5 listed
//	{"want":[]}                                             // nothing, the connection ends
type FileRequest struct {
	Want []string `json:"want"`
}

// the type each result file is sent with; nobody reads it
const resultMediaType = "application/octet-stream"

// After an answer with status done and a non-empty files list, the exchange goes on
// on the same connection. Each wanted file is a normal blob transfer, one after another:
//
//	dispatcher -> executor   {"want":["model.pt","metrics.json"]}   (a result frame)
//	executor   -> dispatcher manifest of model.pt                   (blob.Send / blob.Receive)
//	dispatcher -> executor   yes, if it matches the listed name, size and sha256
//	executor   -> dispatcher model.pt's chunks
//	                         ... the same for metrics.json
//
// a dispatcher that hangs up instead of sending want just gets no files

// checks a want list against the files a task listed, before anything is sent
// every name must be listed, and only once
//
//	listed [model.pt metrics.json]
//	want   [metrics.json model.pt]         → nil, any order
//	want   []                              → nil, nothing to send
//	want   [model.pt model.pt]             → "task: file \"model.pt\" wanted twice"
//	want   [../../tsnet/tailscaled.state]  → "task: file \"../../tsnet/tailscaled.state\" is not one of this task's files"
func checkWant(listed []ResultFile, want []string) (map[string]ResultFile, error) {
	byName := make(map[string]ResultFile, len(listed))
	for _, f := range listed {
		byName[f.Name] = f
	}
	seen := make(map[string]bool, len(want))
	for _, name := range want {
		if _, ok := byName[name]; !ok {
			return nil, fmt.Errorf("task: file %q is not one of this task's files", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("task: file %q wanted twice", name)
		}
		seen[name] = true
	}
	return byName, nil
}

// the executor's half after the answer: reads the want list and sends each wanted file from dir
// a sender that hangs up instead of asking is not an error
//
//	resp lists [model.pt metrics.json], dir results/a7d40e19-…
//	want [model.pt]                   → sends model.pt, nil
//	connection closed, no want        → nil, nothing sent
//	want [secret.txt]                 → error, nothing sent; the dispatcher sees the connection close
//	dispatcher answers no to model.pt → *RefusedError, e.g. "file changed since the run"; the rest aren't sent
func serveFiles(rw io.ReadWriter, files []ResultFile, dir string, onProgress blob.ProgressFunc) error {
	var req FileRequest
	if err := readResultFrame(rw, &req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if _, err := checkWant(files, req.Want); err != nil {
		return err
	}
	for _, name := range req.Want {
		path := filepath.Join(dir, name)
		// only a plain file, so a link put there after the run can't send something else from this machine
		if fi, err := os.Lstat(path); err != nil {
			return fmt.Errorf("task: result file %q: %w", name, err)
		} else if !fi.Mode().IsRegular() {
			return fmt.Errorf("task: result file %q is not a plain file", name)
		}
		m, err := blob.BuildManifest(path, resultMediaType, 0)
		if err != nil {
			return err
		}
		if err := blob.Send(rw, path, m, onProgress); err != nil {
			return fmt.Errorf("task: send result file %q: %w", name, err)
		}
	}
	return nil
}

// the dispatcher's half after an answer that listed files: asks for want and saves each file into dir
// each file is written as name.part and renamed once its whole hash checks out
// so dir never holds half a file under the real name
//
//	resp.Files [model.pt(10 MB) optimizer.pt(20 MB) metrics.json], want [model.pt metrics.json]
//	    → dir/model.pt and dir/metrics.json, optimizer.pt never sent
//	executor sends a model.pt whose size or sha256 differs from its list
//	    → refused before any chunk, "task: model.pt doesn't match the listed file …"
//	connection drops halfway through model.pt
//	    → error, no model.pt or model.pt.part left behind
func FetchResultFiles(rw io.ReadWriter, files []ResultFile, want []string, dir string, onProgress blob.ProgressFunc) error {
	byName, err := checkWant(files, want)
	if err != nil {
		return err
	}
	// the names come from the other node, so each must already be one plain, safe file name
	for _, name := range want {
		if name == "" || blob.SafeName(name) != name {
			return fmt.Errorf("task: unsafe result file name %q", name)
		}
	}
	if err := writeResultFrame(rw, FileRequest{Want: want}); err != nil {
		return err
	}
	for _, name := range want {
		listed := byName[name]
		final := filepath.Join(dir, name)
		part := final + ".part"
		accept := func(m *blob.Manifest) (string, error) {
			if m.Name != listed.Name || m.Size != listed.Size || m.SHA256 != listed.SHA256 {
				return "", fmt.Errorf("task: %s doesn't match the listed file (got %d bytes %.12s…, listed %d bytes %.12s…)",
					name, m.Size, m.SHA256, listed.Size, listed.SHA256)
			}
			return part, nil
		}
		if _, err := blob.Receive(rw, accept, onProgress); err != nil {
			os.Remove(part)
			return fmt.Errorf("task: receive result file %q: %w", name, err)
		}
		if err := os.Rename(part, final); err != nil {
			os.Remove(part)
			return fmt.Errorf("task: save result file %q: %w", name, err)
		}
	}
	return nil
}
