package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// the name of a way to run a task, e.g. "bare" in a definition's runtime field
type RuntimeName string

const RuntimeBare RuntimeName = "bare"

// the variable a run reads to know where to save files for the sender, e.g. model.pt
//
//	# train.py
//	out = os.environ["EDGEGRID_OUTPUT_DIR"]          # ~/.edgegrid/bravo/results/a7d40e19-…
//	torch.save(model.state_dict(), os.path.join(out, "model.pt"))
const OutputDirEnv = "EDGEGRID_OUTPUT_DIR"

// one kind of task: what to fetch and what to run, one entry in tasks.json
//
//	{"kind":"hello","runtime":"bare","entrypoint":["echo","hello world"]}   // prints "hello world\n"
//	{"kind":"echo","runtime":"bare","entrypoint":["cat"]}                   // prints back the task's input
//	{"kind":"needs-model","runtime":"bare","entrypoint":["true"],
//	 "artifacts":[{"url":"https://…","sha256":"…","dest":"model.bin"}]}     // loads, but Supports says false
//	{"kind":"x","entrypoint":["true"]}                                      // New fails: "executor: \"x\" has no runtime"
type Definition struct {
	Kind       string      `json:"kind"`
	Runtime    RuntimeName `json:"runtime"`
	Artifacts  []Artifact  `json:"artifacts,omitempty"`
	Entrypoint []string    `json:"entrypoint"`
}

// a file a run needs before it starts, checked by its hash
type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Dest   string `json:"dest"`
}

// a way to run a task's program, e.g. as a plain process (Bare)
// env holds extra variables on top of the node's own, e.g. ["EDGEGRID_OUTPUT_DIR=/home/bravo/.edgegrid/bravo/results/a7d40e19-…"]
type Runtime interface {
	Name() RuntimeName
	Run(ctx context.Context, workDir string, entrypoint []string, input []byte, env []string) ([]byte, error)
}

// finds the definition for a task kind and runs it with the matching runtime
type Executor struct {
	defs     map[string]Definition
	runtimes map[RuntimeName]Runtime
	workRoot string // each run gets a fresh dir under here, removed afterwards, e.g. ~/.edgegrid/<profile>/runs
}

// reads the list of task kinds from a JSON file, e.g. ~/.edgegrid/<profile>/tasks.json
func LoadDefinitions(path string) ([]Definition, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("executor: read registry: %w", err)
	}
	var defs []Definition
	if err := json.Unmarshal(b, &defs); err != nil {
		return nil, fmt.Errorf("executor: parse registry %s: %w", path, err)
	}
	return defs, nil
}

func New(defs []Definition, runtimes []Runtime, workRoot string) (*Executor, error) {
	e := &Executor{
		defs:     make(map[string]Definition, len(defs)),
		runtimes: make(map[RuntimeName]Runtime, len(runtimes)),
		workRoot: workRoot,
	}
	for _, r := range runtimes {
		e.runtimes[r.Name()] = r
	}
	for _, d := range defs {
		if err := d.validate(); err != nil {
			return nil, err
		}
		if _, dup := e.defs[d.Kind]; dup {
			return nil, fmt.Errorf("executor: kind %q defined twice", d.Kind)
		}
		e.defs[d.Kind] = d
	}
	return e, nil
}

func (d Definition) validate() error {
	switch {
	case d.Kind == "":
		return errors.New("executor: definition with empty kind")
	case d.Runtime == "":
		return fmt.Errorf("executor: %q has no runtime", d.Kind)
	case len(d.Entrypoint) == 0:
		return fmt.Errorf("executor: %q has no entrypoint", d.Kind)
	}
	return nil
}

// whether this node can run the kind right now
// fetching artifacts isn't built yet, so a kind that needs them is refused
//
//	"hello"          → true
//	"resnet"         → false, not in tasks.json
//	"needs-model"    → false, it lists artifacts
//	"in-container"   → false, its runtime "container" isn't one this node has
func (e *Executor) Supports(kind string) bool {
	d, ok := e.defs[kind]
	if !ok || len(d.Artifacts) > 0 {
		return false
	}
	_, ok = e.runtimes[d.Runtime]
	return ok
}

// runs the kind once; a panic in the runtime becomes an error, not a crashed node
// each run works in its own empty folder, e.g. ~/.edgegrid/primary/runs/run-1234567, removed afterwards
// files meant for the sender go in outDir instead, which the run is told about and which is kept
//
//	Run(ctx, "hello", nil, "")                    → "hello world\n", nil
//	Run(ctx, "echo", {"x":1}, "")                 → {"x":1}, nil
//	Run(ctx, "train", nil, ".../results/a7d40e19-…") → "epoch 5 loss 0.03\n", nil; model.pt is left in outDir
//	Run(ctx, "fail", nil, "")                     → nil, "bare: [sh -c echo boom >&2; exit 3]: exit status 3 (stderr: \"boom\\n\")"
//	Run(ctx, "resnet", nil, "")                   → nil, "executor: kind \"resnet\" not supported on this node"
//	prints more than 1 MiB                        → nil, "bare: [...]: output exceeded 1048576 bytes"
//	ctx cancelled mid-run                         → nil, "bare: [sleep 30]: signal: killed (stderr: \"\")"
func (e *Executor) Run(ctx context.Context, kind string, input []byte, outDir string) (out []byte, err error) {
	if !e.Supports(kind) {
		return nil, fmt.Errorf("executor: kind %q not supported on this node", kind)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := e.defs[kind]
	rt := e.runtimes[d.Runtime]

	if err := os.MkdirAll(e.workRoot, 0o700); err != nil {
		return nil, fmt.Errorf("executor: work root: %w", err)
	}
	workDir, err := os.MkdirTemp(e.workRoot, "run-")
	if err != nil {
		return nil, fmt.Errorf("executor: work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("executor: %s panicked: %v", kind, r)
		}
	}()
	var env []string
	if outDir != "" {
		env = append(env, OutputDirEnv+"="+outDir)
	}
	return rt.Run(ctx, workDir, d.Entrypoint, input, env)
}
