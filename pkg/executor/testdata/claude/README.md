# Claude stream provenance fixtures

These fixtures retain the fields that distinguish display text from CLI
diagnostics in Claude Code `--output-format stream-json --verbose` output. IDs,
usage, tool lists, timestamps, and other unrelated metadata are minimized.

| Source | Representative shape | Pattern authority |
| --- | --- | --- |
| Normal assistant text | `type:"assistant"` with text in `message.content[]`; no `error` or `is_api_error_message:true` | Display output only on a clean run, even when it quotes a configured phrase. |
| Successful result | `type:"result"`, `subtype:"success"`, `is_error:false`, and a string `result` summary | Confirms a clean structured outcome. The summary duplicates already-streamed assistant text. |
| Result/error | `type:"result"`, `is_error:true`, and diagnostic text in string `result`; observed subtypes include `error_during_execution`. | Trusted diagnostic metadata. Do not infer success from `subtype` alone. |
| Authentication failure | One or more `type:"system"`, `subtype:"api_retry"` records with `error_status:401` and `error:"authentication_failed"`, followed by an assistant record with `error:"authentication_failed"` and `is_api_error_message:true`, then an error result. | `api_retry` records are excluded from diagnostics entirely — they report an attempt, not an outcome. The assistant/error-result records carry the same failure and are the trusted diagnostics. The observed terminal result has `subtype:"success"`, `is_error:true`, `terminal_reason:"api_error"`, and `api_error_status:401`. |
| Rate-limit failure | `type:"rate_limit_event"`, followed by an assistant record with `error:"rate_limit"` and `is_api_error_message:true`, then a result with `is_error:true`, `terminal_reason:"api_error"`, `api_error_status:429`, and the diagnostic in `result`. | Trusted diagnostic records. Claude Code 2.1.251 was observed emitting `subtype:"success"` on this error result, so `is_error` and the error fields are authoritative. |
| Non-JSON stderr | A plain line because `execClaudeRunner` merges stderr into stdout before parsing. | Trusted CLI diagnostic input; it remains surfaced verbatim. |
| Process exit failure | No stream event. `CommandRunner.Run` supplies it through the `wait` error after the stream is parsed. | Makes surfaced failed-run output eligible for pattern checks; without a match, preserve the process error. |

`diagnostic-rate-limit.jsonl` and `diagnostic-authentication.jsonl` are minimized
from Claude Code 2.1.251 captures on 2026-08-29. Authentication was exercised
with an invalid, process-local API key. The narrated fixtures model normal
assistant and successful result records while deliberately placing configured
phrases in non-diagnostic text.
