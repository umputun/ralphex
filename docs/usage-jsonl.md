# Usage JSONL Log

Ralphex writes token usage events to a JSONL file next to the progress log.

- progress log: `.ralphex/progress/progress-<name>.txt`
- usage log: `.ralphex/progress/progress-<name>.usage.jsonl`

Each line is a standalone JSON object.

## Events

- `request` - one record per AI request
- `summary_total` - aggregate totals for the full run
- `summary_tool` - aggregate totals per tool (`claude`, `codex`, `custom`)

## Schema

```json
{
  "timestamp": "2026-03-26T13:44:21.887442Z",
  "event": "request",
  "tool": "claude",
  "provider": "claude",
  "model": "default",
  "phase": "review",
  "iteration": 2,
  "usage": {
    "InputTokens": 1234,
    "OutputTokens": 321,
    "TotalTokens": 1555,
    "CacheRead": 0,
    "CacheWrite": 0
  }
}
```

Notes:

- `phase` and `iteration` are set for `request` records only.
- usage extraction is best-effort. missing or unparseable usage does not fail execution.
- JSONL write errors are logged as warnings and do not fail execution.
