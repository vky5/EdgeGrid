package node

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/task"
)

// the folder holding the files each task produced, one folder per task, inside the data folder
//
//	~/.edgegrid/bravo/results/
//	    a7d40e19-5c2b-4f86-9e13-62b8d0f4c5a7/    // train-mnist, done
//	        model.pt                             // 4.2 MB, waiting for alpha5-297b to fetch it
//	        metrics.json
//	    3f2b8c1e-9a4d-4c71-b2e5-7d1f0a6c9e38/    // hello: made no files, so the folder was removed
const taskResultsDir = "results"

// where one task's files go, as a full path, since the run works from a different folder
// the task ID is already checked to be a UUID, so it is safe as a folder name
//
//	dataDir "./data2", task a7d40e19-…  → "/home/bravo/edgegrid/data2/results/a7d40e19-…"
func resultsDirFor(dataDir, taskID string) (string, error) {
	return filepath.Abs(filepath.Join(dataDir, taskResultsDir, taskID))
}

// lists the plain files a run left in its results folder, with their size and hash
// folders and links are skipped and logged: a link could point at any file on this machine
//
//	results/a7d40e19-…/model.pt         4200000 bytes   → listed
//	results/a7d40e19-…/metrics.json     812 bytes       → listed
//	results/a7d40e19-…/checkpoints/     folder          → skipped, "task: a7d40e19-…: skipping checkpoints, not a plain file"
//	results/a7d40e19-…/passwd → /etc/passwd   link      → skipped the same way
//	results/a7d40e19-…/.env                             → skipped, the sender would refuse that name
//	empty folder                                        → nil, nil
func listResultFiles(taskID, dir string) ([]db.TaskFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read results folder: %w", err)
	}
	var files []db.TaskFile
	for _, e := range entries {
		if !e.Type().IsRegular() {
			log.Printf("task: %s: skipping %s, not a plain file", taskID, e.Name())
			continue
		}
		// the sender only saves names that are already safe file names, e.g. not ".env" or a 200-letter name
		if blob.SafeName(e.Name()) != e.Name() {
			log.Printf("task: %s: skipping %q, the sender couldn't save that name", taskID, e.Name())
			continue
		}
		f, err := hashFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		f.Name = e.Name()
		files = append(files, f)
	}
	return files, nil
}

// reads a file once for its size and sha256
func hashFile(path string) (db.TaskFile, error) {
	fh, err := os.Open(path)
	if err != nil {
		return db.TaskFile{}, fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	defer fh.Close()
	h := sha256.New()
	n, err := io.Copy(h, fh)
	if err != nil {
		return db.TaskFile{}, fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	return db.TaskFile{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// deletes task rows older than a week, and the results folders of tasks this node ran for others
// runs each time a task is accepted, so a node that never gets tasks never cleans up
// if this node crashes between the two, the folder stays behind; it holds no secrets, only disk space
//
//	results/a7d40e19-…/model.pt   received, done 8 days ago   → row and folder deleted
//	results/3f2b8c1e-…/model.pt   received, done 2 days ago   → kept, the sender may still fetch it
//	results/c91e…/model.pt        sent, fetched 8 days ago    → row deleted, folder kept for its owner
func (a *Node) pruneOldResults() {
	ids, err := a.history.PruneTasks(time.Now().Add(-task.ResultRetention))
	if err != nil {
		log.Printf("task: prune old results: %v", err)
		return
	}
	for _, id := range ids {
		dir, err := resultsDirFor(a.cfg.DataDir, id)
		if err == nil {
			err = os.RemoveAll(dir)
		}
		if err != nil {
			log.Printf("task: %s: could not delete old results folder: %v", id, err)
		}
	}
}

// another ask for the same task is already downloading its files, e.g. the live wait and a background check
var errFetchInProgress = errors.New("this task's files are already being fetched")

// the folder a sent task's files are saved in, e.g. /home/alpha/.edgegrid/alpha/results/a7d40e19-…
// "" if the path can't be worked out
func (a *Node) ResultsDir(taskID string) string {
	dir, err := resultsDirFor(a.cfg.DataDir, taskID)
	if err != nil {
		return ""
	}
	return dir
}

// downloads a done task's files into results/<task-id>/ on the connection that just got the answer
// files already there with the same size and hash are skipped, so asking twice doesn't download twice
//
//	listed [model.pt metrics.json], folder empty            → wants both
//	listed [model.pt metrics.json], model.pt already saved  → wants only metrics.json
//	listed [model.pt metrics.json], both already saved      → wants nothing, done at once
//	another ask is fetching this task right now              → errFetchInProgress, nothing sent
//	model.pt is 2 GB, 1 GB free                              → "not enough free space …", nothing sent
//
// why a.fetching is needed: the live wait gives up at 10 min and background checks start at 10 min,
// so their asks don't overlap, but a download that is already running does
//
//	case 1, live download runs past the 10-minute mark
//	09:00     task sent; first background check due 09:10
//	09:09:50  live wait asks → done, [model.pt 2 GB] → claims a7d40e19, starts a ~10 min download
//	09:10     live wait's time is up, but its download keeps going
//	09:10     background check due → done → a7d40e19 already in fetching → errFetchInProgress, tries later
//
//	case 2, background against background
//	09:10     worker 1 claims the row, next check 09:30 → done → claims a7d40e19, starts a 25 min download
//	09:30     row still running, so the feeder hands it to worker 2 → done → errFetchInProgress, tries later
//	09:35     worker 1 finishes; the next check finds the files already here and downloads nothing
func (a *Node) fetchResultFiles(conn net.Conn, began time.Time, taskID string, files []task.ResultFile) error {
	if _, busy := a.fetching.LoadOrStore(taskID, struct{}{}); busy {
		return errFetchInProgress
	}
	defer a.fetching.Delete(taskID)

	dir, err := resultsDirFor(a.cfg.DataDir, taskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("results folder: %w", err)
	}

	var want []string
	var need int64
	for _, f := range files {
		if have, err := hashFile(filepath.Join(dir, f.Name)); err == nil && have.Size == f.Size && have.SHA256 == f.SHA256 {
			continue
		}
		want = append(want, f.Name)
		need += f.Size
	}
	if len(want) > 0 {
		if err := checkFreeSpace(dir, need); err != nil {
			return err
		}
		log.Printf("task: %s: fetching %d file(s), %s, into %s", taskID, len(want), formatBytes(uint64(need)), dir)
	}

	// the 10s for the answer is over; now 60s with nothing moving, 30 minutes at most
	refreshDeadline(conn, began, idleTimeout, resultFilesTimeout)
	progress := func(blob.Progress) { refreshDeadline(conn, began, idleTimeout, resultFilesTimeout) }
	return task.FetchResultFiles(conn, files, want, dir, progress)
}

// refuses a download that won't fit with 256 MB to spare; if free space can't be read it goes ahead
func checkFreeSpace(dir string, size int64) error {
	free, err := freeBytesFunc(dir)
	if err != nil {
		log.Printf("task: could not read free space in %s, not checking: %v", dir, err)
		return nil
	}
	if need := uint64(size) + spaceHeadroom; free < need {
		return fmt.Errorf("not enough free space: need %s (files %s plus %s spare), have %s",
			formatBytes(need), formatBytes(uint64(size)), formatBytes(spaceHeadroom), formatBytes(free))
	}
	return nil
}
