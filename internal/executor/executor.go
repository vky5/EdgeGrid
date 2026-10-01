package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// RuntimeName identifies a Runtime, e.g. in a Definition's "runtime" field.
type RuntimeName string

const RuntimeBare RuntimeName = "bare"

// Definition describes one kind of task: what to fetch and what to run.
type Definition struct {
	Kind       string      `json:"kind"`
	Runtime    RuntimeName `json:"runtime"`
	Artifacts  []Artifact  `json:"artifacts,omitempty"`
	Entrypoint []string    `json:"entrypoint"`
}

// Artifact is a file a run needs before it starts, pinned by its hash.
type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Dest   string `json:"dest"`
}

// Runtime is the mechanism that actually runs an entrypoint.
type Runtime interface {
	Name() RuntimeName
	Run(ctx context.Context, workDir string, entrypoint []string, input []byte) ([]byte, error)
}

// Executor looks up a kind's Definition and hands it to the matching Runtime.
type Executor struct {
	defs     map[string]Definition
	runtimes map[RuntimeName]Runtime
	workRoot string // each run gets a fresh dir under here, removed afterwards
}

// LoadDefinitions reads a registry file: a JSON array of Definitions.
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

// Supports reports whether this node can run kind right now. Artifact
// fetching isn't built yet, so a kind that needs artifacts is unsupported.
func (e *Executor) Supports(kind string) bool {
	d, ok := e.defs[kind]
	if !ok || len(d.Artifacts) > 0 {
		return false
	}
	_, ok = e.runtimes[d.Runtime]
	return ok
}

// Run executes kind once. A panicking runtime becomes an error, not a crashed node.
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
