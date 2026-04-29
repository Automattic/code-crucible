# External Fixtures

Code Crucible uses HTTP fixture files to make mock, replay, and recorded evaluation deterministic.

Runs created with `--external-mode allowlist`, `--external-mode mock`, `--external-mode replay`, or `--external-mode record` archive a fixture file at:

```text
external/http-fixtures.json
```

If `--external-fixtures PATH` is provided, Code Crucible validates that JSON file and copies it into the run archive. If no fixture path is provided, Code Crucible creates an empty template. The run's `external/policy.json` points to the archived fixture file so the run remains reproducible.

## Format

```json
{
  "version": 1,
  "fixtures": [
    {
      "id": "get-users",
      "request": {
        "method": "GET",
        "url": "https://api.example.com/users",
        "headers": {
          "accept": "application/json"
        }
      },
      "response": {
        "status": 200,
        "headers": {
          "content-type": "application/json"
        },
        "body": "[]"
      }
    }
  ]
}
```

Required fields:

- `version`: currently `1`
- `fixtures[].id`: stable unique fixture identifier
- `fixtures[].request.method`: HTTP method
- `fixtures[].request.url`: full request URL
- `fixtures[].response.status`: HTTP status code

Optional fields:

- `fixtures[].request.headers`
- `fixtures[].request.body_sha256`
- `fixtures[].response.headers`
- `fixtures[].response.body`

## Mock Gateway

Runs with fixture-backed or allowlist modes also archive:

```text
external/mock-gateway.go
external/mock-ca.pem
external/mock-ca-key.pem
```

This is the first generated gateway artifact. During sandboxed `allowlist`, `mock`, `replay`, and `record` evaluation, Code Crucible exports:

- `CRUCIBLE_EXTERNAL_MODE`
- `CRUCIBLE_HTTP_FIXTURES`
- `CRUCIBLE_MOCK_GATEWAY_SOURCE`
- `CRUCIBLE_MOCK_GATEWAY_ADDR`
- `CRUCIBLE_MOCK_GATEWAY_URL`
- `CRUCIBLE_MOCK_CA_CERT`
- `CRUCIBLE_MOCK_CA_KEY`
- `CRUCIBLE_ALLOWED_HOSTS`
- `CRUCIBLE_EXTERNAL_TRACE`
- `CRUCIBLE_RECORD_FIXTURES`
- `HTTP_PROXY` / `http_proxy`
- `HTTPS_PROXY` / `https_proxy`
- `NO_PROXY` / `no_proxy`
- `SSL_CERT_FILE`
- `REQUESTS_CA_BUNDLE`
- `CURL_CA_BUNDLE`
- `NODE_EXTRA_CA_CERTS`
- `GIT_SSL_CAINFO`

Code Crucible starts the gateway on `CRUCIBLE_MOCK_GATEWAY_ADDR` before invoking `evaluator.sh`, and stops it after the evaluator exits. Container runs start it inside the archived resource wrapper; local runs start the same archived gateway from the host. When container evaluation needs the gateway, Code Crucible builds a Linux `external/mock-gateway-<goos>-<goarch>` binary from the archived source on the host and passes it as `CRUCIBLE_MOCK_GATEWAY_BIN`, so the sandbox image does not need Go just to run the gateway. If that binary is missing, the wrapper falls back to `go run external/mock-gateway.go`. The default URL is:

```text
http://127.0.0.1:18080
```

In `allowlist` mode, HTTP clients that honor proxy environment variables are forwarded only when the request host appears in `CRUCIBLE_ALLOWED_HOSTS`; other hosts receive a gateway denial. An empty `CRUCIBLE_ALLOWED_HOSTS` value is valid and means no live outbound hosts are allowed. HTTPS clients that honor `HTTPS_PROXY` use a normal `CONNECT` tunnel to allowlisted hosts. In `mock` and `replay` modes, HTTP clients that honor standard proxy environment variables can call the original `http://...` URL from the fixture, and the request will route through the mock gateway. HTTPS clients that honor `HTTPS_PROXY` and the exported trust variables can call the original `https://...` URL; the gateway handles `CONNECT`, terminates TLS with a run-local test CA, and serves the matching fixture. In `record` mode, proxied HTTP traffic is forwarded to live upstream hosts, summarized in `external-trace.json`, and captured in `recorded-http-fixtures.json` beside the candidate.

For HTTP clients that do not honor proxy environment variables but can be pointed at a base URL, the gateway also supports direct-routed requests:

```text
http://127.0.0.1:18080/__crucible/http/api.example.com/users
http://127.0.0.1:18080/__crucible/https/api.example.com/users
```

The gateway maps those paths back to `http://api.example.com/users` or `https://api.example.com/users` for fixture lookup, allowlist checks, recording, and trace output. Evaluators can also send `X-Crucible-Target-URL` or a `crucible_url` query parameter when rewriting paths is easier.

`mock-ca-key.pem` is a generated test-only private key scoped to the run archive. Do not install this CA globally or reuse it outside the evaluation sandbox.

Current limits:

- Clients that honor proxy environment variables are routed automatically. Clients that ignore proxy variables can use direct-routed gateway URLs, or Docker/Podman `--external-routing gateway-network` when their target hostnames are declared by `--allow-hosts` or archived HTTP fixtures.
- HTTPS replay depends on the client trusting the exported mock CA variables; some runtimes may require evaluator-specific trust configuration.
- Record mode captures HTTP response bodies from proxied HTTP requests; HTTPS CONNECT tunnels are traced as tunnel events but their encrypted payloads are not converted into replay fixtures yet.
- Local gateway-backed evaluation still cannot block unrelated host-network access or transparently intercept raw sockets. Evaluation reports warn when local mode cannot enforce this boundary; use Docker/Podman `--external-routing gateway-network` when clients cannot use proxy variables or direct-routed gateway URLs.
