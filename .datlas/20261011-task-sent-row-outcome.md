---
id: 20261011-task-sent-row-outcome
title: recordSentResult writes the finished status onto the sent row, mapping unknown to failed
level: detail
system: tasks
status: proposed
date: 2026-10-11 00:33
tags: []
realizes: [20261013-task-send-to-shown-result]
supersedes: []
---
## Decision
Only a finished result is written to A's sent row, through `RecordTaskFinish(RoleSent, ...)` in `recordSentResult` (node/task_await.go).

## Example
1. B returns done with `hello`: A's row becomes done with output `hello`.
2. B returns failed or interrupted: A copies the status and error text.
3. B returns unknown (pruned after 7 days, or B's database was wiped): A stores failed, "the executing node has no record of this task".
4. A's row is missing because `recordSentTask` failed earlier: `ErrTaskNotFound` is ignored silently.
5. Gap: `AwaitResult` times out and the row stays running. On A's next restart `ReconcileStaleTasks` marks it interrupted, "node restarted", because it ignores role, even if B finished.
6. Gap: `PruneTasks` only runs after A accepts an incoming task, so a node that only sends never prunes its sent rows.

## Why
- Unknown means the result is gone for good, so leaving the row running would be wrong forever.
- A missing sent row was already logged at dispatch, so logging again adds noise.

## Alternatives rejected
- **Store unknown as its own status**: the table only knows running, done, failed and interrupted.

## Diagram
```d2
direction: right

running: sent running
done: done
failed: failed
intr: interrupted

running -> done: B says done
running -> failed: B says failed or unknown
running -> intr: B says interrupted
running -> intr: A restarts, role ignored {style.stroke-dash: 4}
```
