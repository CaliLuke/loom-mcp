# Anthropic attachment limits

Verified 2026-10-11 against official provider documentation using network access.

- https://platform.claude.com/docs/en/build-with-claude/vision#request-limits
  currently documents 10 MB per base64-encoded image for the direct API (5 MB
  on partner platforms), and PNG/JPEG/GIF/WebP. K22a retains the frozen contract's
  conservative 5,000,000 raw-byte cap; this is a framework cap, not a claim that
  today's direct API limit is 5 MB. Decimal MB is used conservatively.
- https://platform.claude.com/docs/en/build-with-claude/pdf-support#check-pdf-requirements
  documents a 32 MB whole-request limit, including other content. The raw PDF
  ceiling is 24,000,000 bytes (32,000,000 * 3/4), accounting for base64 expansion.
  This is an individual-attachment ceiling, not a guarantee that a complete
  request fits: JSON framing, other attachments, tools and conversation content
  consume the same request budget. The existing model clone budget can impose a
  smaller aggregate limit. K22a does not change that budget.

Tests use the real SDK against a local HTTP server; the bytes need not be valid
media because serialization, ordering and preflight are the contracts tested.
