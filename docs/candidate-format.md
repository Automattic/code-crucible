# Candidate Format

Code Crucible stores every competitor as a directory under a round directory.

For the first round:

```text
.crucible/runs/<run-id>/round-0001/
  candidate-0000-baseline/
  candidate-0001/
  candidate-0002/
```

`candidate-0000-baseline` is created from the original project source. Generated competitors must use the `candidate-NNNN` form, starting at `candidate-0001`.

## Required Files

Every generated candidate must include:

```text
candidate-NNNN/
  candidate.json
  design.md
  src/
```

`src/` contains the drop-in replacement implementation.

`design.md` explains the implementation approach, expected tradeoffs, known risks, and any assumptions made while preserving the documented interface contract.

`candidate.json` contains machine-readable metadata:

```json
{
  "id": "candidate-0001",
  "name": "short descriptive name",
  "round": 1,
  "parent_ids": ["candidate-0000-baseline"],
  "agent": "codex",
  "model": "gpt-5.5",
  "source_path": "src",
  "baseline": false,
  "created_at": "2026-04-27T20:30:00Z"
}
```

Required fields:

- `id`
- `name`
- `round`
- `parent_ids`
- `agent`
- `source_path`
- `baseline`

`created_at` should be an RFC3339 UTC timestamp. If it is omitted, adoption records the current time in the leaderboard entry.

## Adoption

`crucible adopt` scans the current round directory and adds valid generated candidates to `leaderboard.json`.

Adoption validates:

- Directory name matches `candidate-NNNN`
- `candidate.json` exists and parses
- `candidate.json.id` matches the directory name
- `design.md` exists
- `src/` exists
- `source_path` resolves to the candidate's `src/` directory
- Generated candidates are not marked as baseline

Malformed candidates are reported and skipped. Adoption does not silently repair candidate artifacts.
