---
id: 20261013-task-executor-run
title: The executor runs a registered kind as a plain process in a scratch dir, input on stdin, output from stdout
level: detail
system: tasks
status: proposed
date: 2026-10-13 22:42
tags: []
realizes: [20261011-task-submit-to-stored-result]
supersedes: []
---
## Decision
`Executor.Run` (executor/executor.go) looks up the kind's definition and hands it to that definition's runtime. The only runtime today is `Bare` (executor/bare.go), a plain process with the node's own privileges.

## Example
1. At startup `loadExecutor` (node/task_executor.go) reads `<data-dir>/tasks.json`, say `[{"kind":"hello","runtime":"bare","entrypoint":["echo","hello"]}]`. Missing or invalid means no kinds.
2. `Supports("hello")` is true only if the kind exists, lists no artifacts (fetching is not built) and its runtime is registered.
3. `Run` creates a fresh `runs/run-*` dir under the data dir and deletes it afterwards.
4. `Bare.Run` starts `echo hello` in that dir with the task's input on stdin.
5. Stdout is kept up to 1MB and stderr up to 4KB. A non-zero exit becomes an error that carries the stderr.
6. Stdout over 1MB fails the run. Between 64KB and 1MB it fails later, when the row is stored.
7. Node shutdown cancels the context: the process is killed, and `WaitDelay` stops waiting on held pipes after 5 seconds.
8. A panicking runtime is recovered into an error. Task requirements (CPU, memory, GPU) are only checked for negatives, never matched.

## Why
- Output caps stop one chatty process exhausting memory.
- `Runtime` is the seam for an isolated runtime later; `Bare` gives none.

## Diagram
```d2
direction: down

def: Definition for kind
sup: Supports kind?
dir: Fresh run dir
proc: Process, input on stdin
exit: Exit code zero?
cap: Stdout within 1MB?
out: Return stdout
fail: Return error {style.stroke-dash: 4}

def -> sup
sup -> dir: yes
sup -> fail: no
dir -> proc
proc -> exit
exit -> cap: yes
exit -> fail: no, with stderr
cap -> out: yes
cap -> fail: no
```
