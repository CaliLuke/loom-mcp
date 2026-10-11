# Models API response body

`context_limits.json` is a saved wire-format Models.Get response fixture, built
from the documented schema and verified limits, not a live credentialed capture.

Schema: https://platform.claude.com/docs/en/api/models/retrieve
Token figures: https://platform.claude.com/docs/en/models/overview
Verified 2026-10-10: Claude Opus 5.5, 1,000,000 context and 128,000 output.
The SDK decoding test maps max_input_tokens to the input allowance plus the
configured default output reserve; the shared-window table uses 1,000,000.
