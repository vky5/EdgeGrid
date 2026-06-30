package executor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

const venvCacheDir = "/tmp/edgegrid-venvs"

// TrainingExecutor runs the user's Python training script inside an isolated
// venv. Venvs are cached by SHA256(requirements.txt) so repeated jobs with
// the same dependencies skip the install step.
type TrainingExecutor struct{}

func NewTrainingExecutor() *TrainingExecutor {
	return &TrainingExecutor{}
}

func (e *TrainingExecutor) Execute(ctx context.Context, req *workerpb.TrainingJobRequest, jobDir string) error {
	if len(req.TrainingScript) == 0 {
		return fmt.Errorf("training_script is empty")
	}

	inputDir := filepath.Join(jobDir, "input")
	outputDir := filepath.Join(jobDir, "output")

	for _, dir := range []string{inputDir, outputDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create dir %s: %w", dir, err)
		}
	}

	scriptPath := filepath.Join(inputDir, "train.py")
	if err := os.WriteFile(scriptPath, req.TrainingScript, 0644); err != nil {
		return fmt.Errorf("failed to write training script: %w", err)
	}

	python, err := e.resolveVenv(ctx, req.Requirements, inputDir)
	if err != nil {
		return fmt.Errorf("venv setup failed: %w", err)
	}

	return e.runScript(ctx, python, scriptPath, outputDir, req.JobId, req.TrainingConfigJson)
}

// resolveVenv returns the path to a Python interpreter.
// If requirements is empty, falls back to system python3/python.
// Otherwise creates (or reuses from cache) a venv keyed by SHA256(requirements).
func (e *TrainingExecutor) resolveVenv(ctx context.Context, requirements, inputDir string) (string, error) {
	if requirements == "" {
		return findSystemPython()
	}

	hash := sha256.Sum256([]byte(requirements))
	venvDir := filepath.Join(venvCacheDir, fmt.Sprintf("%x", hash))
	sentinel := filepath.Join(venvDir, ".ready")
	python := venvPythonPath(venvDir) // resolve python path inside venv

	if _, err := os.Stat(sentinel); err == nil {
		log.Printf("venv cache hit: %s", venvDir)
		return python, nil
	}

	log.Printf("creating venv at %s", venvDir)

	sysPython, err := findSystemPython()
	if err != nil {
		return "", err
	}

	// if hash doesnt match
	if err := runCmd(ctx, sysPython, "-m", "venv", venvDir); err != nil {
		return "", fmt.Errorf("failed to create venv: %w", err)
	}

	reqPath := filepath.Join(inputDir, "requirements.txt")
	if err := os.WriteFile(reqPath, []byte(requirements), 0644); err != nil {
		return "", fmt.Errorf("failed to write requirements.txt: %w", err)
	}

	pip := venvPipPath(venvDir)
	if err := runCmd(ctx, pip, "install", "-r", reqPath); err != nil {
		return "", fmt.Errorf("pip install failed: %w", err)
	}

	if err := os.WriteFile(sentinel, []byte("ready"), 0644); err != nil {
		return "", fmt.Errorf("failed to write venv sentinel: %w", err)
	}

	return python, nil
}

func (e *TrainingExecutor) runScript(ctx context.Context, python, scriptPath, outputDir, jobID, configJSON string) error {
	cmd := exec.CommandContext(ctx, python, scriptPath)
	cmd.Dir = filepath.Dir(scriptPath)
	cmd.Env = append(os.Environ(),
		"OUTPUT_DIR="+outputDir,
		"JOB_ID="+jobID,
		"TRAINING_CONFIG="+configJSON,
	)

	lw := &logWriter{prefix: fmt.Sprintf("[job %s] ", jobID)}
	cmd.Stdout = lw
	cmd.Stderr = lw

	log.Printf("starting training script for job %s", jobID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("training script exited with error: %w", err)
	}
	log.Printf("training script completed for job %s", jobID)
	return nil
}

func (e *TrainingExecutor) Close() error { return nil }

// findSystemPython returns the path to python3 or python on PATH.
func findSystemPython() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("python3/python not found on PATH")
}

// venvPythonPath returns the python binary path inside a venv.
func venvPythonPath(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(venvDir, "bin", "python")
}

// venvPipPath returns the pip binary path inside a venv.
func venvPipPath(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "pip.exe")
	}
	return filepath.Join(venvDir, "bin", "pip")
}

// runCmd runs a command and pipes its output to the logger.
func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &logWriter{prefix: "[setup] "}
	cmd.Stderr = &logWriter{prefix: "[setup] "}
	return cmd.Run()
}

// logWriter pipes subprocess output line-by-line to the Go logger.
type logWriter struct {
	prefix string
}

func (lw *logWriter) Write(p []byte) (int, error) {
	log.Printf("%s%s", lw.prefix, p)
	return len(p), nil
}
