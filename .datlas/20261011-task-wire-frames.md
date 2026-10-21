---
id: 20261011-task-wire-frames
title: Offer, verdict and result travel as version-prefixed length-framed JSON
level: detail
system: tasks
status: accepted
date: 2026-10-11 00:10
tags: []
realizes: [20261011-task-submit-to-stored-result]
supersedes: []
---
## Decision
Every task message after the hello is one frame: a version byte, a 4-byte big-endian length, then a JSON body. The reader checks the version before decoding and the length before allocating.

## Example
1. A's `Offer` calls `WriteTask` with `{"id":"<uuid>","kind":"echo","input":{...}}` (task/task.go). The kind is illustrative.
2. B's `ProcessTask` (task/task_process.go) calls `ReadTask`, which rejects a wrong version or a length over 64KB (`maxTaskSize`).
3. `Validate` checks the uuid, kind and requirements, then `accept` decides.
4. The verdict reuses blob's `WriteVerdict`: accept, or refuse with a reason string.
5. On the result connection A's `AskResult` sends `{"task_id":"<uuid>"}` and B's `ServeResult` replies `{"status","output","error"}` (task/result.go).
6. Result frames cap at 128KB (`maxResultSize`) so a 64KB output, about 87KB as base64, fits.

## Why
- A version outside the JSON catches a schema mismatch before decoding.
- Length caps stop a peer forcing large allocations.
- Reusing the blob verdict keeps file and task refusals identical.

## Alternatives rejected
- **A task-specific verdict format**: no gain over the blob one.

## Diagram
```d2
shape: sequence_diagram
A: Sender A
B: Node B

c1: Task connection {
  A -> B: hello, intent task
  A -> B: offer frame, max 64KB
  B -> A: verdict frame
}
c2: Result connection {
  A -> B: hello, intent result
  A -> B: request frame, task_id
  B -> A: response frame, max 128KB
}
```
