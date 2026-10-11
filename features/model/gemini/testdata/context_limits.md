# Models API response body

`context_limits.json` is a saved wire-format Models.Get response fixture, built
from the documented schema and verified limits, not a live credentialed capture.

Schema: https://ai.google.dev/api/models
Token figures: https://ai.google.dev/gemini-api/docs/models/gemini-2.5-flash
Verified 2026-10-10: input 1,048,576, output 65,536. This fixture ID is not an
eligible table key under decision 5; the adapter discovers it only through Get.
