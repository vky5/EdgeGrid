# Grid Access — Job-Submission Allowlist

## Why this exists

Before this feature, GitHub sign-in was the entire gate on the dashboard. Anyone with a GitHub account could sign in and submit training jobs to the grid — including someone who had never contributed a worker themselves. Node approval ([access-control.md](./access-control.md), [worker-approval.md](./worker-approval.md)) only ever controlled whether a *machine* could join NATS as a worker; it said nothing about whether the *person* behind a GitHub login could dispatch jobs through the dashboard.

Grid access is a second, independent allowlist that closes that gap: a GitHub user can submit jobs only once they've been explicitly granted access, either by contributing an approved worker or by a direct admin grant.

## What it does

- `internal/usermgr` stores an `approved_users` JetStream KV bucket, keyed by GitHub username.
- `POST /api/jobs` (the Next.js route) checks this allowlist before forwarding a submission to the coordinator — admins always pass, everyone else needs a grant (`web/lib/coordinator.ts:isApprovedUser`).
- A grant can happen two ways: automatically, as a side effect of one of the user's claimed nodes being approved; or directly, via an admin action that requires no node at all.

## Deliberately not the same list as node approval

It would have been simpler to just check "does this GitHub user have an approved node" directly at job-submission time, instead of maintaining a second KV bucket. That was considered and rejected: it would mean the admin (or anyone else) needs to run a dummy worker just to unlock their own ability to submit jobs, and it would mean a node going offline or being reformatted silently revokes the owner's dashboard access too, since submission rights would be *derived live* from node status rather than recorded as their own fact. `usermgr` is a one-time grant recorded independently — approving a node grants access once, but losing that node afterward doesn't take it back.

## Step-by-step flow

### Path A — earn access by contributing a worker

1. A node is claimed by a GitHub user (`POST /join/claim/{nodeID}`, see [access-control.md](./access-control.md) for the join/claim mechanics).
2. An admin approves the node (`joinapi.Approve`, `internal/coordinator/joinapi/joinapi.go`). As a side effect:

```go
// internal/coordinator/joinapi/joinapi.go — Approve
if req.GitHubUsername != "" {
    if grantErr := um.Approve(req.GitHubUsername, "node:"+nodeID); grantErr != nil {
        log.Printf("warning: failed to auto-grant dashboard access for %s: %v", req.GitHubUsername, grantErr)
    }
}
```

3. There's an out-of-order case this also has to cover: an admin might approve a node *before* its operator gets around to claiming it. `handleJoinClaim` checks for this and grants immediately instead of waiting on a re-approval that would never come (`Approve` on an already-approved node returns a 409):

```go
// internal/coordinator/joinapi/joinapi.go — Claim
if req, err := jm.Get(nodeID); err == nil && req.Status == joinmgr.StatusApproved {
    if grantErr := um.Approve(body.GitHubUsername, "node:"+nodeID); grantErr != nil {
        log.Printf("warning: failed to auto-grant dashboard access for %s: %v", body.GitHubUsername, grantErr)
    }
}
```

### Path B — direct admin grant, no node required

An admin can grant access straight from the `/nodes` page's "GRID ACCESS" panel, or by calling the endpoint directly:

```
POST /admin/users/{username}/approve
```

which calls `usermgr.Approve(username, "admin")`. This exists specifically so the admin isn't forced to run a throwaway worker just to submit their own jobs, and so a trusted friend can be let in before their machine is fully set up.

### Enforcement point

```go
// web/app/api/jobs/route.ts — POST
if (!(await isApprovedUser(user))) {
  return NextResponse.json({ error: 'grid access pending admin approval' }, { status: 403 })
}
```

`isApprovedUser` short-circuits to `true` for admins; everyone else triggers a coordinator lookup:

```go
// internal/coordinator/router.go — route registration
mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
    // GET /users/{username}/status
    usersapi.Status(w, parts[0], um)  // internal/coordinator/usersapi/usersapi.go
})
```

### Idempotency

`Approve` is a no-op if the username is already in the KV — re-approving an already-granted user (e.g. approving a second node they later claim) does not overwrite the original `approved_via` reason. The first grant reason sticks.

```go
// internal/usermgr/usermgr.go
func (m *Manager) Approve(username, via string) error {
    if _, err := m.kv.Get(username); err == nil {
        return nil // already approved — keep the original reason
    }
    ...
}
```

## Migration note

This gate only applies going forward from when it was introduced. Any node that was already approved-and-claimed before `usermgr` existed does not retroactively grant its owner access — the KV only gets written on new approvals/claims. Use the direct admin-grant path (`POST /admin/users/{username}/approve`, or the "GRANT DIRECTLY" box on `/nodes`) to backfill anyone who should already have access.

## What's not built

- **No revocation UI.** `usermgr.Revoke` exists as a method but there's no admin-panel button wired to it yet — removing someone's access currently means calling the coordinator endpoint by hand (there isn't one for revoke either; it would need to be added alongside a UI action).
- **No audit trail beyond `approved_via`/`approved_at`.** There's no record of *who* (which admin) issued a direct grant, only that it was granted "via admin."
