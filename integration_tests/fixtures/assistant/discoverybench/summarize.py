"""Recompute metrics from retained raw evidence without invoking either retriever."""
import argparse
import json
from pathlib import Path

from scoring import canonical, summarize

ROOT = Path(__file__).parent


def report(output):
    catalog = json.loads((ROOT / "testdata/catalog.json").read_bytes())
    cases = json.loads((ROOT / "testdata/cases.json").read_bytes())["cases"]
    recordings = [json.loads((output / (name + ".json")).read_bytes()) for name in ("baseline", "schemarouter")]
    summary = summarize(cases, catalog, recordings)
    imported = json.loads((output / "imported_catalog.json").read_bytes())
    endpoints = {item["name"]: item for item in imported["endpoints"]}
    diagnostics = []
    for source in catalog["tools"]:
        target = endpoints[source["name"]]
        diagnostics.append({"tool": source["name"],
                            "input_schema_equal": source["inputSchema"] == target["input_schema"],
                            "output_schema_equal": source["outputSchema"] == target["output_schema"],
                            "source_meta": source.get("_meta"), "imported_metadata": target["metadata"],
                            "typed_parameters": target["parameters"], "typed_output_fields": target["output_fields"]})
    summary["import_diagnostics"] = diagnostics
    (output / "summary.json").write_bytes(canonical(summary))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("output", type=Path)
    report(parser.parse_args().output)
