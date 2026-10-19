"""Run the pinned, unmodified MCP importer and stock synchronous retriever."""
import argparse
import hashlib
import importlib.metadata
import json
from pathlib import Path
import platform
import sys
import time

from schemarouter import SchemaRouter
from schemarouter.adapters.mcp import tool_from_mcp

from scoring import canonical

ROOT = Path(__file__).parent
EXPECTED = {
    "catalog": "5533903d33ecbb42f6523b353ba9bfb8c60c0a705ee499c9b28223736a66a94b",
    "cases": "a248254ef490c931f2284648a1c97f5a796b6b368f752c1081247ccbd020a62b",
}


def load_frozen(name):
    raw = (ROOT / "testdata" / (name + ".json")).read_bytes()
    if hashlib.sha256(raw).hexdigest() != EXPECTED[name]:
        raise ValueError("frozen corpus changed: " + name)
    return json.loads(raw)


def run(output):
    catalog = load_frozen("catalog")
    cases = load_frozen("cases")
    config = json.loads((ROOT / "config.json").read_text())
    if platform.python_version() != config["python"]:
        raise ValueError("use the pinned Python version")
    if (config["warmups"], config["samples"]) != (20, 100):
        raise ValueError("observation configuration changed")
    started = time.perf_counter_ns()
    imported = tool_from_mcp(config["server_name"], catalog)
    router = SchemaRouter()
    router.add_tool(imported)
    setup_ns = time.perf_counter_ns() - started
    imported_json = imported.model_dump(mode="json")
    output.mkdir(parents=True, exist_ok=True)
    (output / "imported_catalog.json").write_bytes(canonical(imported_json))
    rows = []
    for case in cases["cases"]:
        for k in cases["k_values"]:
            durations = []
            variants, sample_variants, warmup_variants = [], [], []
            variant_indexes = {}
            for sample in range(config["warmups"] + config["samples"]):
                started = time.perf_counter_ns()
                result = router.retrieve(case["query"], k=k)
                duration = time.perf_counter_ns() - started
                current = result.model_dump(mode="json")
                current_raw = result.model_dump_json().encode("utf-8")
                if current_raw not in variant_indexes:
                    variant_indexes[current_raw] = len(variants)
                    variants.append({"payload": current, "native_payload": current_raw.decode("utf-8"),
                                     "native_payload_bytes": len(current_raw)})
                index = variant_indexes[current_raw]
                if sample >= config["warmups"]:
                    durations.append(duration)
                    sample_variants.append(index)
                else:
                    warmup_variants.append(index)
            rows.append({"case_id": case["id"], "k": k, "variants": variants,
                         "sample_variants": sample_variants, "warmup_variants": warmup_variants,
                         "latency_ns": durations})
    discovery = router.retrieve("analyze_sentiment", k=1).model_dump(mode="json")
    recording = {"arm": "schemarouter", "python": sys.version, "platform": platform.platform(),
                 "version": importlib.metadata.version("schemarouter"), "setup_ns": setup_ns,
                 "results": rows, "host_boundary_discovery": discovery}
    (output / "schemarouter.json").write_bytes(canonical(recording))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("output", type=Path)
    run(parser.parse_args().output)
