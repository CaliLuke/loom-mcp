// Package judge labels claims against already rendered evaluation subjects.
// It is independent of eval and trajectory: callers supply the subject kind,
// text, task and shared reference, then map the resulting judgments themselves.
//
// New requires a model.Client, ModelID and positive MaxOutputTokens. Requests use
// Complete with the private record_judgments tool forced, temperature zero and
// no thinking. The Codex adapter cannot be used because it rejects MaxTokens.
// Live clients should set Config.Live to require LOOM_LIVE_MODEL=1; scripted
// clients leave it false. The package never enables live access automatically.
//
// Claim IDs and subject kinds match [a-z0-9_]{1,64}. Invalid inputs, schemas over
// 1 MiB and complete encoded requests over MaxInputBytes fail before any call,
// including calibration. MaxInputBytes defaults to 512 KiB and also applies to
// correction requests. Text is never silently cut. Set Input.Truncated when a
// renderer elides content; the prompt and Result both carry that fact. Whitespace
// subjects produce not_addressed labels and NoCall without calibration or judging.
//
// Responses must contain one record_judgments call with exactly the requested
// claim keys, valid labels and non-empty UTF-8 rationales. Strict JSON v2 rejects
// duplicate and unknown members. At most three corrections follow the first
// call. Raw-client corrections carry the invalid call and error tool results;
// validated-client rejections carry only the OutputValidationError kind. Results
// preserve input claim order. Usage and Latency include corrections, excluding
// calibration; a failed call may return a partial Result alongside its error.
//
// The first non-empty Judge call calibrates once per Judge, shared by concurrent
// callers. Four built-ins cover the labels; Config.Examples appends labeled
// application cases. Each example gets one call, without corrections, within a
// shared CalibrationTimeout (default two minutes). Only exact label agreement
// passes. Completed successes and failures are cached by model, output cap,
// prompt hash and examples. Infrastructure failures are not cached; judging
// continues with Calibrated false. A Judge retains its calibration outcome;
// create a new Judge to retry, using Recalibrate to bypass a completed cache entry.
//
// The default cache is process-wide. NewMemoryCache isolates a suite; NewDirCache
// persists completed calibrations in atomically replaced <hex>.json files. T15
// can place the directory at the experiment store's _calibration path without
// coupling this package to that store. Judgments themselves are never cached.
package judge
