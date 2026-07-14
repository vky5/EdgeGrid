package executor

import (
	"bytes"
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
type TrainingExecutor struct {
	logPublish func(jobID, line string) // optional; publishes each stdout/stderr line to NATS
}

func NewTrainingExecutor(logPublish func(jobID, line string)) *TrainingExecutor {
	return &TrainingExecutor{logPublish: logPublish}
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
	// -u disables Python's output buffering so each print() flushes immediately
	// instead of being batched into one chunk when the process exits.
	cmd := exec.CommandContext(ctx, python, "-u", scriptPath)
	cmd.Dir = filepath.Dir(scriptPath)
	// PATH here isn't for finding `python` above (we already have its full
	// path) — it's so the script itself can shell out to console-scripts
	// pip installed into this venv's bin/, e.g. `subprocess.run(["gdown"])`.
	// Those wrapper scripts have a shebang pointing back at this same
	// python, so once found via PATH, they resolve their own package
	// imports through this interpreter's venv-scoped site-packages.
	cmd.Env = append(allowlistedEnv(python),
		"OUTPUT_DIR="+outputDir,
		"JOB_ID="+jobID,
		"TRAINING_CONFIG="+configJSON,
	)

	var pub func([]byte)
	if e.logPublish != nil {
		pub = func(p []byte) { e.logPublish(jobID, string(p)) }
	}
	lw := &logWriter{prefix: fmt.Sprintf("[job %s] ", jobID), publish: pub}
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

// allowlistedEnv passes only PATH/HOME instead of the full os.Environ(),
// so worker secrets don't leak to arbitrary training scripts. python's
// bin/ is prepended to PATH — the one thing `source venv/bin/activate` does.
func allowlistedEnv(python string) []string {
	var env []string
	path := filepath.Dir(python)
	if sysPath := os.Getenv("PATH"); sysPath != "" {
		path += string(os.PathListSeparator) + sysPath
	}
	env = append(env, "PATH="+path)
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}
	return env
}

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

// logWriter pipes subprocess output line-by-line to the Go logger and
// optionally to a publish func (e.g. NATS JetStream for live log streaming).
// Write splits on newlines so each line is a separate log entry and a
// separate NATS message — SSE requires one logical line per data: field.
type logWriter struct {
	prefix  string
	publish func([]byte)
}

func (lw *logWriter) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		log.Printf("%s%s", lw.prefix, line)
		if lw.publish != nil {
			lw.publish(line)
		}
	}
	return len(p), nil
}
