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

// one kind of task: what to fetch and what to run
// e.g. {"kind":"hello","runtime":"bare","entrypoint":["echo","hello world"]}
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
type Runtime interface {
	Name() RuntimeName
	Run(ctx context.Context, workDir string, entrypoint []string, input []byte) ([]byte, error)
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
func (e *Executor) Supports(kind string) bool {
	d, ok := e.defs[kind]
	if !ok || len(d.Artifacts) > 0 {
		return false
	}
	_, ok = e.runtimes[d.Runtime]
	return ok
}

// runs the kind once; a panic in the runtime becomes an error, not a crashed node
func (e *Executor) Run(ctx context.Context, kind string, input []byte) (out []byte, err error) {
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
	return rt.Run(ctx, workDir, d.Entrypoint, input)
}
