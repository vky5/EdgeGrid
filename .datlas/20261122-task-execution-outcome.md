---
id: 20261122-task-execution-outcome
title: A run ends as done, failed, or lost when the node closes mid-run
level: detail
system: tasks
status: accepted
date: 2026-11-22 01:06
tags: []
realizes: [20261011-task-submit-to-stored-result]
supersedes: [20261011-task-execution-outcome]
---
## Decision
After accepting, `receiveTask` runs the task in `a.runs.Go` with `a.runCtx` (node/task_receive.go) and `finishTask` stores one of three outcomes. A run cut short by the node closing is lost, not failed.

## Example
1. bravo runs `hello` and it prints `hello from B`: the row becomes done with that output.
2. bravo runs a kind whose command exits 1: failed, `bare: [false]: exit status 1 (stderr: "")`.
3. Ctrl+C on bravo during `sleep 30`: `Close` cancels `runCtx`, the process is killed, and the row becomes lost, `bare: [sleep 30]: signal: killed (stderr: "")`.
4. The command prints 100 KB: `RecordTaskFinish` (db/tasks.go) stores failed, "output exceeded 65536 bytes", never cut short.
5. If the finish write itself fails, the row stays running until the next start marks it lost, "node restarted".

## Why
- Lost tells the sender a resend makes sense; failed says the task itself broke.
- `Close` waits on `a.runs`, so the lost row is written before the database closes.

## Alternatives rejected
- **Keep "interrupted" for a closing node**: it means the same as lost for the sender.
- **Truncate large output**: the sender would trust a wrong result.

## Diagram
```d2
direction: down
run: Run returns
err: Returned an error?
closing: Node closing?
size: Output over 64 KB?
done: done, output stored
failed: failed, error text
big: failed, output too big
lost: lost, error text

run -> err
err -> closing: yes
closing -> lost: yes
closing -> failed: no
err -> size: no
size -> big: yes
size -> done: no
```
