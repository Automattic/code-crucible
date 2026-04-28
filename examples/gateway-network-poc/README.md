# Gateway Network PoC

This example verifies container `gateway-network` routing with a fixture-backed evaluator that ignores proxy environment variables.

The evaluator makes direct raw-socket requests to:

- `http://api.crucible.test/v1/status`
- `https://secure.crucible.test/v1/status`

Both hosts are declared in `fixtures/http-fixtures.json`, so Code Crucible should map them to the gateway sidecar inside the Podman or Docker evaluator network. The HTTPS request trusts the run-local mock CA through the standard certificate environment variables exported by the evaluator.

Run the Podman proof of concept from the repository root:

```bash
make smoke-gateway-network-podman
```
