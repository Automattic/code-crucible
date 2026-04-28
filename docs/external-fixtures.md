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

This is the first generated gateway artifact. It can serve matching fixtures, but evaluator integration and proxy routing are still being built. Until that wiring lands, evaluators that need mock or replay behavior should start the gateway explicitly and configure the candidate under test to call it.
