# Container Raw Socket Routing Design

Code Crucible supports raw socket routing only inside Docker or Podman evaluator sandboxes. Local mode remains advisory because the framework should not mutate host firewall, DNS, or routing state.

## Goals

- Keep local evaluation safe and non-invasive.
- Route containerized evaluator traffic through the archived mock gateway when clients ignore proxy environment variables.
- Preserve comparable tournament results by keeping every candidate in a run on the same sandbox engine and routing mode.
- Record enough metadata to reproduce the evaluator network, gateway command, and routing rules.

## Non-Goals

- Transparent interception for local host processes.
- Host-wide firewall, `iptables`, `nftables`, or TProxy changes.
- Cross-host or cluster networking.
- Silent routing for arbitrary external hosts not declared in the run policy, fixtures, or discovery handoff.

## Mode

Use the explicit container routing mode:

```bash
crucible evaluate \
  --sandbox-engine docker \
  --sandbox-image golang:1.25 \
  --external-routing gateway-network
```

The mode is valid only with Docker or Podman. Local evaluation should reject it with an actionable message.

## Network Shape

For each candidate evaluation:

1. Create a short-lived, user-defined isolated bridge network.
2. Start a gateway sidecar on that network from archived gateway artifacts.
3. Start the evaluator container on the same network.
4. Route declared external hostnames to the gateway sidecar.
5. Tear down both containers and the network after evaluation.

The evaluator container does not join the default bridge network. For Docker, Code Crucible uses a user-defined bridge network rather than the default bridge. For Podman, it uses an equivalent per-evaluation bridge network. `mock` and `replay` networks are created with `--internal`; `allowlist` and `record` networks allow upstream access for gateway passthrough.

## Routing Rules

The implementation routes only declared hosts:

- `--allow-hosts`
- hosts present in archived HTTP fixtures
- hosts named in structured discovery handoffs

Hostnames are mapped to the gateway sidecar inside the evaluator network with container host entries. Requests to undeclared hosts fail closed in `mock` and `replay` because those networks are internal. `allowlist` and `record` route declared hosts through the sidecar, but undeclared live hosts may remain reachable when upstream network access is required.

The gateway sidecar listens on standard HTTP and HTTPS ports inside the evaluator network. If binding privileged ports is not viable for the selected engine/rootless mode, the candidate evaluation fails with a setup error rather than silently falling back to partial proxy-only routing.

## Archived Metadata

Each candidate evaluation should archive:

- selected external routing mode
- sandbox engine and image
- evaluator network name
- gateway container name or ID
- gateway command and artifact path
- hostname mappings
- teardown status
- warnings when routing was partial or unavailable

## Failure Behavior

- Local mode plus raw socket routing: fail before evaluation.
- Container engine unavailable: fail before evaluation.
- Gateway sidecar startup failure: fail the candidate evaluation.
- Hostname mapping unsupported by the engine/profile: fail closed.
- Teardown failure: record a warning and include the orphaned resource name.

## Test Plan

- Unit-test command construction for Docker and Podman network creation, gateway sidecar startup, evaluator run, and teardown.
- Integration-test with a small HTTP client that ignores proxy environment variables.
- Verify undeclared hosts fail in `mock`/`replay`.
- Verify allowlisted hosts can pass through only in `allowlist` or `record`.
- Verify local mode rejects raw socket routing.
- Verify archived evaluation reports include routing metadata and warnings.
