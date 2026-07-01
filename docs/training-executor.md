# Training Executor — Local Job Execution Pipeline

## What it does

The training executor is the piece that actually runs user code on a worker machine. It takes a `TrainingJobRequest` — which contains a Python script, a `requirements.txt`, and a config blob — and produces a trained model checkpoint in an isolated output directory. It handles three things automatically: setting up a Python virtual environment with the right dependencies, running the script with injected environment variables, and routing all output back to the coordinator via NATS.

---

## Why a venv per job would be wrong

The naive approach: create a fresh venv for every job, install dependencies, run the script, delete the venv. This works but is slow. `pip install torch` can take 3–5 minutes. If ten jobs in a row all require `torch==2.0.0`, you'd install it ten times.

EdgeGrid caches venvs by the SHA256 hash of `requirements.txt`. If the hash matches an existing venv directory, the install step is skipped entirely. The trade-off: venvs accumulate on the worker's disk. This is acceptable — disk is cheap, pip install time is not.

---

## The TrainingExecutor struct

```go
// internal/worker/executor/training.go

type TrainingExecutor struct {
    logPublish func(jobID, line string)
}

func NewTrainingExecutor(logPublish func(jobID, line string)) *TrainingExecutor {
    return &TrainingExecutor{logPublish: logPublish}
}
```

`logPublish` is optional. When the executor is created with a publish func (in production), every line the training script prints is forwarded to NATS. When it is nil (e.g. in tests), output stays local only. The executor has no knowledge of NATS directly — it receives a plain function.

---

## Step 1 — Execute entry point

```go
func (e *TrainingExecutor) Execute(ctx context.Context, req *workerpb.TrainingJobRequest, jobDir string) error {
    if len(req.TrainingScript) == 0 {
        return fmt.Errorf("training_script is empty")
    }

    inputDir  := filepath.Join(jobDir, "input")
    outputDir := filepath.Join(jobDir, "output")

    for _, dir := range []string{inputDir, outputDir} {
        os.MkdirAll(dir, 0755)
    }

    scriptPath := filepath.Join(inputDir, "train.py")
    os.WriteFile(scriptPath, req.TrainingScript, 0644)

    python, err := e.resolveVenv(ctx, req.Requirements, inputDir)
    if err != nil {
        return fmt.Errorf("venv setup failed: %w", err)
    }

    return e.runScript(ctx, python, scriptPath, outputDir, req.JobId, req.TrainingConfigJson)
}
```

`jobDir` is the isolated working directory for this job — created by `runTrainingPipeline` in `listener.go` as `/tmp/edgegrid-jobs/<jobID>/`. It is deleted when the job finishes via `defer os.RemoveAll(jobDir)`. `Execute` creates `input/` and `output/` subdirectories inside it, writes the training script to `input/train.py`, then resolves the Python interpreter.

---

## Step 2 — Venv resolution and caching

```go
func (e *TrainingExecutor) resolveVenv(ctx context.Context, requirements, inputDir string) (string, error) {
    if requirements == "" {
        return findSystemPython()  // no requirements → use system python3
    }

    hash := sha256.Sum256([]byte(requirements))
    venvDir  := filepath.Join(venvCacheDir, fmt.Sprintf("%x", hash))
    sentinel := filepath.Join(venvDir, ".ready")
    python   := venvPythonPath(venvDir)

    if _, err := os.Stat(sentinel); err == nil {
        log.Printf("venv cache hit: %s", venvDir)
        return python, nil  // cache hit — skip everything
    }
    ...
}
```

`venvCacheDir` is `/tmp/edgegrid-venvs`. For a given `requirements.txt` content, SHA256 produces a deterministic 64-character hex string. The venv lives at `/tmp/edgegrid-venvs/<hash>/`.

**The `.ready` sentinel file** is the cache validity signal. It is only written after pip install succeeds. If the process crashes during install, the sentinel does not exist — the next job with the same requirements will redo the install from scratch rather than using a half-installed venv.

```
/tmp/edgegrid-venvs/
  a3f9c1.../        ← SHA256("torch==2.0.0\nnumpy==1.24.0\n")
    bin/
      python        ← the interpreter
      pip
    lib/
      ...
    .ready          ← sentinel: only present after successful pip install
```

### Cache miss path

```go
    sysPython, _ := findSystemPython()

    runCmd(ctx, sysPython, "-m", "venv", venvDir)  // create empty venv

    reqPath := filepath.Join(inputDir, "requirements.txt")
    os.WriteFile(reqPath, []byte(requirements), 0644)

    pip := venvPipPath(venvDir)
    runCmd(ctx, pip, "install", "-r", reqPath)      // install dependencies

    os.WriteFile(sentinel, []byte("ready"), 0644)   // mark complete

    return python, nil
```

`runCmd` pipes stdout and stderr to a `logWriter` with prefix `[setup]`. These logs print to the worker's terminal but are **not** published to NATS — the job submitter does not need to see pip's output. Only the training script's output goes to NATS.

### Python path resolution

```go
func venvPythonPath(venvDir string) string {
    if runtime.GOOS == "windows" {
        return filepath.Join(venvDir, "Scripts", "python.exe")
    }
    return filepath.Join(venvDir, "bin", "python")
}
```

The interpreter path differs between Windows (`Scripts/python.exe`) and Unix (`bin/python`). The venv structure follows Python's standard layout — no assumptions about the venv tool version.

---

## Step 3 — Running the training script

```go
func (e *TrainingExecutor) runScript(ctx context.Context, python, scriptPath, outputDir, jobID, configJSON string) error {
    cmd := exec.CommandContext(ctx, python, scriptPath)
    cmd.Dir = filepath.Dir(scriptPath)  // working directory = input/
    cmd.Env = append(os.Environ(),
        "OUTPUT_DIR="     + outputDir,
        "JOB_ID="         + jobID,
        "TRAINING_CONFIG=" + configJSON,
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
```

**`exec.CommandContext`** attaches the job's cancellable context to the OS process. When the context is cancelled (via `DELETE /jobs/{id}`), Go sends SIGKILL to the Python process immediately. `cmd.Run()` then returns an error wrapping `context.Canceled`.

**Environment variables** injected into the training script:

| Variable | Value | Purpose |
|---|---|---|
| `OUTPUT_DIR` | `/tmp/edgegrid-jobs/<jobID>/output` | Training script writes model files here |
| `JOB_ID` | `abc123` | Script can use for logging or naming artifacts |
| `TRAINING_CONFIG` | `{"epochs":10,"lr":0.001,...}` | Arbitrary JSON config from the job request |

The script reads these at runtime:
```python
import os, json

output_dir = os.environ["OUTPUT_DIR"]
config     = json.loads(os.environ["TRAINING_CONFIG"])

# ... train ...

model.save(os.path.join(output_dir, "model.pt"))
```

**The logWriter** implements `io.Writer` and is wired to both stdout and stderr:

```go
type logWriter struct {
    prefix  string
    publish func([]byte)
}

func (lw *logWriter) Write(p []byte) (int, error) {
    log.Printf("%s%s", lw.prefix, p)   // always print locally on the worker
    if lw.publish != nil {
        lw.publish(p)                   // also push to NATS for streaming
    }
    return len(p), nil
}
```

Every call to `Write` is one chunk of subprocess output. The `prefix` field (`[job abc123] `) distinguishes lines in the worker's local terminal when multiple things are running.

---

## Full pipeline summary

```
TrainingJobRequest arrives at worker
  │
  ├── Execute(ctx, req, jobDir)
  │     │
  │     ├── write req.TrainingScript → input/train.py
  │     │
  │     ├── resolveVenv(req.Requirements)
  │     │     ├── SHA256(requirements) → hash
  │     │     ├── /tmp/edgegrid-venvs/<hash>/.ready exists?
  │     │     │     YES → return venv/bin/python  (cache hit)
  │     │     │     NO  → python3 -m venv <dir>
  │     │     │           pip install -r requirements.txt
  │     │     │           write .ready sentinel
  │     │     │           return venv/bin/python
  │     │
  │     └── runScript(python, train.py, outputDir)
  │           ├── exec.CommandContext(ctx, python, train.py)
  │           ├── inject OUTPUT_DIR, JOB_ID, TRAINING_CONFIG
  │           ├── stdout/stderr → logWriter → log.Printf + js.Publish
  │           └── cmd.Run() blocks until script exits
  │
  └── output/ contains model checkpoint
        → pushCheckpoint uploads to NATS Object Store
```

---

## Example requirements.txt → cache behaviour

```
Job 1: requirements = "torch==2.0.0\nnumpy==1.24.0"
  SHA256 → a3f9c1...
  /tmp/edgegrid-venvs/a3f9c1.../.ready? NO → pip install (3 min)

Job 2: requirements = "torch==2.0.0\nnumpy==1.24.0"  (same)
  SHA256 → a3f9c1...
  /tmp/edgegrid-venvs/a3f9c1.../.ready? YES → skip install (0 sec)

Job 3: requirements = "torch==2.1.0\nnumpy==1.24.0"  (different torch version)
  SHA256 → b72d44...
  /tmp/edgegrid-venvs/b72d44.../.ready? NO → pip install (3 min)
```

Jobs 1 and 2 share a venv. Job 3 gets its own because even one character difference in `requirements.txt` produces a completely different SHA256 hash.
