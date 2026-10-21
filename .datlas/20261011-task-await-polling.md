---
id: 20261011-task-await-polling
title: AwaitResult polls with doubling backoff for up to 10 minutes and retries network errors
level: detail
system: tasks
status: proposed
date: 2026-10-11 00:32
tags: []
realizes: [20261013-task-send-to-shown-result]
supersedes: []
---
## Decision
`awaitResult` (node/task_await.go) asks, waits 1s doubling to a 10s cap, and stops on any finished status, on 10 minutes, or on the caller cancelling.

## Example
1. Each poll is `askResult`: dial B, hello with the result intent, a 10 second deadline, then `task.AskResult`, then hang up.
2. Poll 1 at 0s: running. Poll 2 after 1s: dial fails, the error is kept and polling continues.
3. Waits grow 1, 2, 4, 8, 10, 10 seconds.
4. A poll returns done, failed, interrupted or unknown: `Finished()` is true and the loop returns it.
5. 10 minutes pass: `ErrAwaitTimeout`, with the last network error attached if there was one.
6. The caller cancels `ctx`: the plain context error, not a timeout.
7. Timing out does not cancel the task on B, which keeps its result for 7 days.

## Why
- Backoff keeps a long task from costing a dial per second.
- The loop is split from dialing so tests drive it with a fake ask and a shrunk `pollPlan`.

## Alternatives rejected
- **Hold one connection open until done**: B's result handler answers once under a 10 second deadline.

## Diagram
```d2
direction: down

ask: askResult
ok: Answer received?
fin: Status finished?
ret: Return result
keep: Keep last error
wait: Wait, then double
over: 10 min or cancelled?
out: Timeout or cancel error {style.stroke-dash: 4}

ask -> ok
ok -> fin: yes
fin -> ret: yes
fin -> wait: running
ok -> keep: no
keep -> wait
wait -> over
over -> ask: no
over -> out: yes
```
