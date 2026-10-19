#!/usr/bin/env bash
# Explicit opt-in command: installs only an isolated environment and rewrites results/.
set -euo pipefail
bench_dir="$(cd "$(dirname "$0")" && pwd)"
fixture_dir="$(cd "$bench_dir/.." && pwd)"
repo_dir="$(cd "$fixture_dir/../../.." && pwd)"
bench_env="${LOOM_DISCOVERY_BENCH_ENV:-/tmp/loom-discoverybench-env}"
uv_bin="${UV:-uv}"
"$uv_bin" venv --python 3.13.9 "$bench_env" --allow-existing
"$uv_bin" pip sync --python "$bench_env/bin/python" "$bench_dir/requirements.lock"
export PYTHONDONTWRITEBYTECODE=1
"$bench_env/bin/python" -m unittest discover -s "$bench_dir" -p '*_test.py'
(cd "$fixture_dir" && go test ./discoverybench -count=1)
mkdir -p "$bench_dir/results"
# Run arms sequentially in a quiet window; do not run builds/tests concurrently.
(cd "$fixture_dir" && LOOM_DISCOVERY_BENCH_RECORD=1 go test ./discoverybench -run '^TestRecordBenchmark$' -count=1)
"$bench_env/bin/python" "$bench_dir/run_schemarouter.py" "$bench_dir/results"
"$bench_env/bin/python" "$bench_dir/summarize.py" "$bench_dir/results"
{
 git -C "$repo_dir" rev-parse HEAD
 git -C "$repo_dir" status --short
 go version
 uname -a
 "$uv_bin" --version
 "$bench_env/bin/python" --version
 "$uv_bin" pip freeze --python "$bench_env/bin/python"
} > "$bench_dir/results/environment.txt"
"$bench_env/bin/python" - "$bench_dir" <<'PY'
import hashlib
import json
from pathlib import Path
import sys
root = Path(sys.argv[1])
files = [*root.glob('*.py'), *root.glob('*.go'), root / 'reproduce.sh', root / 'config.json', root / 'requirements.in', root / 'requirements.lock', *root.glob('testdata/*.json')]
hashes = {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(files)}
fixture = root.parent
for relative in ('go.mod', 'go.sum', 'design/design.go', 'gen/mcp_assistant/adapter_server.go', 'gen/mcp_assistant/local_provider.go', 'gen/mcp_assistant/sdk_server.go'):
    hashes['fixture/' + relative] = hashlib.sha256((fixture / relative).read_bytes()).hexdigest()
(root / 'results/source_hashes.json').write_text(json.dumps(hashes, indent=2, sort_keys=True) + '\n')
PY
cp "$bench_dir/results/source_hashes.json" "$bench_dir/results/capture_source_hashes.json"
