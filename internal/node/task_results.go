package node

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

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
