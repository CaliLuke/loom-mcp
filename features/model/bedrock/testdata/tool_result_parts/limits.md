# Bedrock attachment limits

Verified 2026-10-11 against official provider documentation using network access.

https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Message.html
specifies 3.75 MB per image and 4.5 MB per document. The adapter uses conservative
decimal byte caps of 3,750,000 and 4,500,000. These per-attachment limits do not
replace request-level image/document counts or provider model restrictions.

Tool-result support is restricted to Claude model IDs recognized by claudecaps;
Amazon Nova is excluded by the K22 contract. The installed Bedrock SDK supplies
the image and document tool-result unions and the document-format enumeration.
S3 is the only accepted document URI scheme.

Goldens capture the real AWS SDK's request bodies using a local BaseEndpoint and
static test credentials. No AWS service is contacted by the unit tests.
