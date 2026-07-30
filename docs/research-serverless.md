# Research: serverless P2P for EdgeGrid

Notes from exploring whether EdgeGrid can run with **no dedicated/hosted
servers at all** — just devices on different networks connecting directly to
each other. This is research/notes, not a decision doc — no direction has
been chosen yet.

## Where EdgeGrid already stands

Every node runs the same `cmd/edgegrid` binary; "coordinator" and "worker"
are just optional roles turned on per `config.Config`. A coordinator can
embed its own NATS server (`internal/natsserver`), and other coordinators
cluster into it via NATS's own clustering/gossip using seed `Routes`. There
is no company-operated cloud service anywhere in the design today — the open
question is only about the **network layer** underneath this: how do two
nodes on two different home networks (both probably behind NAT) find and
reach each other at all.

## Why "just add WireGuard" doesn't solve it

WireGuard is a tunnel protocol only — it encrypts traffic between two peers
once both sides already know how to reach each other. It has no discovery
mechanism. Two laptops behind two different home routers can't just start
talking: both are behind NAT, translating private IPs to a shared public
one, and neither router has a standing rule to forward inbound traffic to
that laptop. WireGuard alone doesn't create that path — something else has
to solve discovery and NAT traversal first.

## Core concepts involved

- **NAT types** — Full Cone, Restricted Cone, Port-Restricted Cone,
  Symmetric — determine whether hole-punching can succeed at all.
  **CGNAT** (carrier-grade NAT, common on mobile and some ISPs) makes
  traversal much harder or impossible, since the "NAT" isn't even under the
  user's control.
- **STUN** — lets a device ask an external server "what does my traffic
  look like from the outside" (its public IP:port).
- **TURN** — a relay of last resort when a direct connection can't be
  established.
- **ICE** — the framework (used by WebRTC) that combines STUN + TURN +
  direct attempts into one negotiation process.
- **UDP hole punching** ("simultaneous open") — both peers send packets
  toward each other's guessed public address at roughly the same time, so
  each NAT believes its own side initiated the outbound flow and allows the
  return traffic. Fails against symmetric NAT, since that NAT type assigns a
  different external port per destination, so the guessed address is wrong.
- **Key exchange** — WireGuard itself uses static Curve25519 keypairs and
  the Noise protocol framework (a Noise_IK handshake) for the tunnel's
  encryption. This is separate from, and doesn't help with, the
  discovery/traversal problem above.

## Options surveyed

### WireGuard alone
Minimal, fast, deliberately has no discovery layer. Can "roam" (update a
peer's known endpoint) once a packet has already gotten through — doesn't
help with first contact. Necessary building block, not sufficient alone.

### Tailscale
WireGuard plus:
- a hosted **control plane** (`login.tailscale.com`) that handles key and
  endpoint discovery, plus ACLs
- a global fleet of **DERP relay servers** as NAT-traversal fallback (still
  end-to-end WireGuard-encrypted even when relayed, so the relay operator
  can't read traffic)

Free "Personal" tier (pricing effective 2026-04-08): up to 6 users,
unlimited devices per user, 50 tagged resources, 1,000
ephemeral-resource-minutes/month — permanently free, not a time-limited
trial.

Tradeoff: not literally serverless — someone's control plane is involved,
just not one EdgeGrid would have to run. **Headscale** is an open-source,
self-hostable reimplementation of that control plane for anyone who doesn't
want the dependency on Tailscale-the-company specifically.

### Uncloud (github.com/psviderski/uncloud)
A real, existing lightweight Docker-host orchestrator. Builds an automatic
WireGuard mesh with peer discovery and NAT traversal between machines, gives
containers routable cross-host IPs, and layers a built-in DNS server for
service-name resolution, plus a bundled Caddy reverse proxy for automatic
TLS.

Its key differentiator from Tailscale: **no control plane at all** — fully
decentralized. A new node joins by direct SSH contact with a machine already
in the mesh; membership and discovery then propagate peer-to-peer from
there. Directly relevant prior art, since it's structurally close to
EdgeGrid's existing join flow (a joining node contacts one already-approved
coordinator).

**Unverified / flagged as a guess, not fact:** exactly how Uncloud achieves
NAT hole-punching with zero coordination server has not been confirmed —
worth digging into before treating it as a template.

## Open question — no direction chosen yet

Three shapes on the table, not yet decided between:

1. Roll a custom STUN/hole-punch/relay layer on top of the existing NATS
   clustering.
2. Embed something Tailscale-like (e.g. the `tsnet` library) — accept a
   hosted control-plane dependency, or self-host it via Headscale.
3. An Uncloud-style fully decentralized join + mesh — closest
   philosophically to EdgeGrid's current "no central server" design.

## Next step

Validate whichever option looks best against EdgeGrid's actual deployment
reality: nodes are consumer laptops/desktops on home networks, likely behind
NAT and possibly CGNAT on some ISPs — not cloud VMs with public IPs. That
constraint is what will rule options in or out, more than any feature
comparison on paper.
