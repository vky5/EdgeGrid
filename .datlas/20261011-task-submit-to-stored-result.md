---
id: 20261011-task-submit-to-stored-result
title: Task goes from sender submit to a stored, fetchable result on the executing node
level: story
system: tasks
status: accepted
date: 2026-10-11 00:07
tags: [acl]
realizes: []
supersedes: []
---
## Decision
Node B records every accepted task as a row before it replies, updates that row when the run ends, and serves it back to the sender over a separate connection.

## Example
1. A offers task `t1` to B: `DispatchTask` dials B with a task hello and sends the ID, kind and input.
2. `handlePeer` (peer.go) routes the task intent to `receiveTask` (task_receive.go), which learns A's identity from Tailscale.
3. `accept` runs its checks, then `RecordTaskStart` writes a running row.
4. `ProcessTask` sends the verdict and the connection closes.
5. In the background the executor runs `t1`, and `finishTask` sets the row to done, failed or interrupted.
6. Later A dials again with a result hello: `receiveResult` calls `ServeResult`, which calls `lookupResult` (task_result.go).
7. B answers with the row's status, output and error, or unknown if `t1` is missing or not A's.
8. If B restarts mid-run, `ReconcileStaleTasks` (node.go) marks the row interrupted.

## Why
- The row exists before the verdict, so a result request always finds it.
- Unknown for foreign or missing IDs stops peers probing task IDs.
- Startup reconcile stops a crashed task looking like it is running forever.

## Alternatives rejected
- **Keep results in memory**: lost on restart, so A could never learn the outcome.

## Diagram
```d2
shape: sequence_diagram
A: Sender A
B: Node B
db: tasks table
ex: Executor

offer: Connection 1: offer {
  A -> B: offer t1
  B -> B: accept checks
  B -> db: insert running row
  B -> A: verdict accept
}
run: Background run {
  B -> ex: run t1
  ex -> B: output or error
  B -> db: set final status
}
fetch: Connection 2: result {
  A -> B: ask for t1
  B -> db: look up t1 for A
  B -> A: status and output
}
restart: Restart {
  B -> db: running becomes interrupted
}
```
