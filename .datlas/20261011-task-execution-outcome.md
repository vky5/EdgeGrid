---
id: 20261011-task-execution-outcome
title: The executor runs in a tracked goroutine and finishTask stores one of three outcomes
level: detail
system: tasks
status: accepted
date: 2026-10-11 00:12
tags: []
realizes: [20261011-task-submit-to-stored-result]
supersedes: []
---
## Decision
After the verdict, `receiveTask` starts `executor.Run` inside `a.runs.Go` with `a.runCtx`, releases the slot when it returns, and records the outcome through `finishTask` (node/task_receive.go).

## Example
1. The kinds come from `tasks.json` in the data dir. `loadExecutor` (node/task_executor.go) never fails, and a missing or bad registry means no kinds.
2. `Run` returns output `hello`: `finishTask` stores done with the output.
3. `Run` returns an error: the row becomes failed with the error text.
4. `Run` errors because the node is closing (`runCtx` cancelled): the row becomes interrupted.
5. Output over 64KB: `RecordTaskFinish` (db/tasks.go) stores failed, "output exceeded 65536 bytes", never truncated.
6. If the finish write itself fails, the row stays running until the next restart, and a log line says so.

## Why
- `Close` waits on `runs`, so no task outlives the node.
- A panicking runtime becomes an error, not a crashed node.

## Alternatives rejected
- **Truncate large output**: the sender would trust a wrong result.

## Diagram
```d2
direction: down

run: executor.Run returns
err: Returned an error?
ctx: Node shutting down?
size: Output over 64KB?
done: done, output stored
failed: failed, error text
too: failed, output exceeded
intr: interrupted

run -> err
err -> ctx: yes
ctx -> intr: yes
ctx -> failed: no
err -> size: no
size -> too: yes
size -> done: no
```
