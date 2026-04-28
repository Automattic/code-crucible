# External Fixtures

Code Crucible uses HTTP fixture files to make mock and replay evaluation deterministic.

Runs created with `--external-mode mock`, `--external-mode replay`, or `--external-mode record` archive a fixture file at:

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

Runs with fixture-backed modes also archive:

```text
external/mock-gateway.go
```

This is the first generated gateway artifact. During sandboxed `mock` and `replay` evaluation, Code Crucible exports:

- `CRUCIBLE_EXTERNAL_MODE`
- `CRUCIBLE_HTTP_FIXTURES`
- `CRUCIBLE_MOCK_GATEWAY_SOURCE`
- `CRUCIBLE_MOCK_GATEWAY_ADDR`
- `CRUCIBLE_MOCK_GATEWAY_URL`

The container resource wrapper starts the gateway on `CRUCIBLE_MOCK_GATEWAY_ADDR` before invoking `evaluator.sh`, and stops it after the evaluator exits. The default URL is:

```text
http://127.0.0.1:18080
```

Evaluators should configure the candidate under test to call `CRUCIBLE_MOCK_GATEWAY_URL` when deterministic HTTP responses are required. The current gateway is generated Go source, so sandbox images must include `go` until Code Crucible ships a packaged gateway binary.

Current limits:

- Transparent routing for arbitrary outbound HTTP clients is not implemented yet.
- HTTPS replay is not implemented yet.
- Local evaluation receives the same environment variables, but Code Crucible does not auto-start the gateway outside the container wrapper yet.
