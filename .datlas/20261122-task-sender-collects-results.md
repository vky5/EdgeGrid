---
id: 20261122-task-sender-collects-results
title: The sender keeps asking about tasks it sent until it gets the answer or a week passes
level: story
system: tasks
status: accepted
date: 2026-11-22 00:55
tags: []
realizes: []
supersedes: []
---
## Decision
If A stops waiting before a task ends, A goes back on a schedule and asks the node that ran it, stores the answer when it comes, and only gives up after a week with no answer.

## Example
1. At 09:00 on A you run `edgegrid task send slow`. bravo accepts, and you press Ctrl+C at 09:01. bravo keeps working.
2. A's dashboard is running. At 09:10 A asks bravo: still running. A leaves the task alone.
3. bravo finishes at 09:25 and keeps the result.
4. At 09:30 A asks again: done, with bravo's output. A's record of `slow` becomes done.
5. Had bravo been off, A would keep asking, further apart each time, up to once a day.
6. After 7 days with no answer, A records lost: "no answer from bravo".
7. Restarting A never marks `slow` lost, since restarting A doesn't stop bravo. `task send` on its own never asks again; only `up` and the dashboard do.

## Why
- bravo never has to dial A back; A reuses the result request bravo already answers.
- bravo keeps results for 7 days, so asking longer can't help.

## Alternatives rejected
- **bravo pushes the result to A**: if A is off, bravo needs the same retry loop.
- **Check only when someone looks**: results would update only when opened.
- **An uptime counter instead of stored times**: it stops while A is off, but bravo's 7 days keep running.

## Diagram
```d2
shape: sequence_diagram
A: Node A
B: bravo

send: Send, then stop waiting {
  A -> B: offer slow
  B -> A: accepted
}
first: 09:10 {
  A -> B: how did slow go
  B -> A: running
}
second: 09:30 {
  A -> B: how did slow go
  B -> A: done with output
}
```
