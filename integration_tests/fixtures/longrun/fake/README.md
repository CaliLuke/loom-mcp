# Offline model profiles

Reader and Summariser use explicit configuration profiles on workflow protocol 19.
Profiles bind raw and normalized model identities for all three roles.
The fake constructors never create live providers.
Unknown configurations and missing model identities fail before harness construction.

Anthropic and Bedrock Anthropic profiles declare their attachment support.
OpenAI and Gemini profiles declare no non-text tool-result support.
The offline profile supports PNG and PDF bytes.
`model.NewClient` and `Reject` preserve these capabilities.

Reader reads an asset descriptor at text offset zero, then requests the original attachment by its one-based part index.
It uses only returned bytes and never reads the scoring specification.
Descriptor-only responses cannot satisfy evidence scoring.
Supporting profiles must recover both attachments through recall.
Zero-support profiles must retain their evidence with one residency event and no threshold compactions.

The fixture uses shipped compaction defaults with explicit non-text residency protection.
There is no special 2,000-token retention allowance for multimodal scenarios.
Named window and recall-pressure stresses remain in scenario configuration.
Synthetic model windows are 1,000,000 tokens for planner, 128,000 for small planner, and 200,000 for summarizer.
These windows preserve a smaller-model transition and accommodate the uncompacted baseline.
Each actual baseline request must contain fewer than 128,000 input tokens.

Materializer `k19c-v4` expands historical base workloads without changing raw pool files.
Ordinary runs double the stored step count. Model-change runs quadruple it.
This workload provides repeated compaction coverage under shipped defaults.
The retained v1 source reproduces the old materialization.
