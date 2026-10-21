---
id: 20261013-task-result-callers
title: The CLI blocks in AwaitResult and the Peers tab waits through cancellable bubbletea commands
level: detail
system: tasks
status: proposed
date: 2026-10-13 22:41
tags: []
realizes: [20261013-task-send-to-shown-result]
supersedes: []
---
## Decision
`runTaskSend` (cmd/edgegrid/main.go) calls `AwaitResult` inline after `DispatchTask`. The Peers tab (tui/dashboard/peers.go) chains `dispatchTaskCmd` then `awaitResultCmd`, each under a context that `esc` cancels.

## Example
1. CLI: `DispatchTask` returns bravo, then `AwaitResult(ctx, bravo, t.ID)` blocks. `ctx` comes from `signal.NotifyContext`, so Ctrl+C cancels the wait.
2. CLI: done writes the raw output to stdout. Any other status goes through `ResultResponse.Err()` (task/result.go) to stderr with exit 1.
3. TUI: enter on the kind calls `startWait` for a fresh context and runs `dispatchTaskCmd`, whose `TaskFuncs.Dispatch` returns the task ID.
4. `taskDispatchedMsg` on accept sets `m.taskID` and runs `awaitResultCmd` with `TaskFuncs.Await`, wired to `nodeAgent.AwaitResult`.
5. `taskResultMsg` with the same `taskID` returns to browsing with `outputPreview` (one line, 200 characters) or the `Err()` text.
6. `esc` runs `cancelFlow`: it calls `stopTask` and clears `taskID`, so a late result is dropped.
7. Gap: `esc` cannot free a peer stuck mid offer, because `task.Offer` has no deadline and only the dial uses the context.
8. Gap: after Ctrl+C or a timeout the sent row stays running until A's next start marks it interrupted.

## Why
- Bubbletea runs each command in its own goroutine but never cancels it, so the tab owns the cancel.
- One `Err()` gives the CLI, the TUI and `recordSentResult` the same wording.

## Alternatives rejected
- **A goroutine in the CLI**: `main` returning kills it, so the CLI would have to block anyway.
- **A goroutine writing the model directly**: races the render loop.

## Diagram
```d2
direction: down

kind: Kind typed, enter
offer: dispatchTaskCmd
acc: Accepted?
wait: awaitResultCmd
res: taskResultMsg
show: Show output or error
err: Show refusal {style.stroke-dash: 4}
esc: esc, cancel and drop {style.stroke-dash: 4}

kind -> offer
offer -> acc
acc -> wait: yes
acc -> err: no
wait -> res
res -> show
offer -> esc: esc
wait -> esc: esc
```
