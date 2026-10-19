"""Lossless, source-schema-relative metrics for the frozen retrieval corpus."""
from collections import Counter
import json
import math
import statistics

ANNOTATIONS = frozenset(("description", "title", "$comment", "examples"))


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")


def structural(value):
    if isinstance(value, dict):
        return {key: structural(item) for key, item in value.items() if key not in ANNOTATIONS}
    if isinstance(value, list):
        return [structural(item) for item in value]
    return value


def at_pointer(schema, pointer):
    value = schema
    for segment in pointer.split("/")[1:]:
        value = value[segment.replace("~1", "/").replace("~0", "~")]
    return value


def required_path(schema, pointer):
    """Record required membership at each object-property ancestor of a field."""
    segments = pointer.split("/")[1:]
    value = schema
    memberships = []
    for index, segment in enumerate(segments):
        if segment == "properties" and index + 1 < len(segments):
            memberships.append(segments[index + 1] in value.get("required", []))
        value = value[segment.replace("~1", "/").replace("~0", "~")]
    return memberships


def field_hit(source, exposed, pointer):
    try:
        return (structural(at_pointer(source, pointer)) == structural(at_pointer(exposed, pointer))
                and required_path(source, pointer) == required_path(exposed, pointer))
    except (KeyError, IndexError, TypeError):
        return False


def has_pointer(schema, pointer):
    try:
        at_pointer(schema, pointer)
        return True
    except (KeyError, IndexError, TypeError):
        return False


def schema_bytes(candidates):
    return sum(len(canonical({"inputSchema": c["inputSchema"], "outputSchema": c["outputSchema"]})) for c in candidates)


def score_case(case, candidates, catalog):
    indexed = {candidate["name"]: candidate for candidate in candidates}
    routes = []
    for route in case["routes"]:
        source = catalog[route["tool"]]
        candidate = indexed.get(route["tool"])
        row = {"tool": route["tool"], "tool_hit": candidate is not None}
        for side in ("input", "output"):
            pointers = route[side + "_fields"]
            exposed = candidate[side + "Schema"] if candidate else {}
            hits = [p for p in pointers if field_hit(source[side + "Schema"], exposed, p)]
            row[side + "_hits"] = len(hits)
            row[side + "_total"] = len(pointers)
            row[side + "_missing"] = [p for p in pointers if p not in hits]
        row["field_recall"] = (row["input_hits"] + row["output_hits"]) / (row["input_total"] + row["output_total"])
        routes.append(row)
    return {
        "tool_hit": any(row["tool_hit"] for row in routes),
        "complete_route": any(row["field_recall"] == 1 for row in routes),
        "best_route_field_recall": max((row["field_recall"] for row in routes), default=None),
        "routes": routes,
        "unsupported_false_suggestion": not case["routes"] and bool(candidates),
        "unsupported_field_exposed": any(has_pointer(c["outputSchema"], p) for c in candidates for p in case["unsupported_output_fields"]),
        "shortlist": [c["name"] for c in candidates],
        "shortlist_size": len(candidates),
        "schema_bytes": schema_bytes(candidates),
    }


def summarize_latency(samples):
    ordered = sorted(samples)
    return {"count": len(samples), "median_ns": statistics.median(samples), "p95_ns": ordered[math.ceil(.95 * len(samples)) - 1]}


def normalize_candidates(arm, payload):
    if arm == "baseline":
        return payload["tools"]
    return [{"name": c["endpoint"], "inputSchema": c["input_schema"], "outputSchema": c["output_schema"]} for c in payload["candidates"]]


def summarize_observation(arm, case, raw, catalog):
    counts = Counter(raw["sample_variants"])
    warmups = Counter(raw["warmup_variants"])
    total = len(raw["sample_variants"])
    if total != len(raw["latency_ns"]) or not total:
        raise ValueError("timing/sample reference mismatch")
    variants = []
    for index, variant in enumerate(raw["variants"]):
        payload = variant["payload"]
        metrics = score_case(case, normalize_candidates(arm, payload), catalog)
        metrics.update({"samples": counts[index], "warmups": warmups[index],
                        "payload_bytes": len(canonical(payload)),
                        "native_payload_bytes": variant["native_payload_bytes"]})
        variants.append(metrics)
    if sum(v["samples"] for v in variants) != total:
        raise ValueError("sample references unknown variant")
    result = {"arm": arm, "case_id": raw["case_id"], "k": raw["k"], "variants": variants,
              "latency": summarize_latency(raw["latency_ns"])}
    for metric in ("tool_hit", "complete_route", "unsupported_false_suggestion", "unsupported_field_exposed"):
        result[metric + "_rate"] = sum(v[metric] * v["samples"] for v in variants) / total
    result["best_route_field_recall_mean"] = (sum(v["best_route_field_recall"] * v["samples"] for v in variants) / total if case["routes"] else None)
    for metric in ("shortlist_size", "schema_bytes", "payload_bytes", "native_payload_bytes"):
        measured = [v[metric] for v in variants if v["samples"]]
        result[metric + "_range"] = [min(measured), max(measured)]
        result[metric + "_mean"] = sum(v[metric] * v["samples"] for v in variants) / total
    return result


def summarize(cases, catalog, recordings):
    by_case = {case["id"]: case for case in cases}
    indexed = {tool["name"]: tool for tool in catalog["tools"]}
    rows = [summarize_observation(recording["arm"], by_case[raw["case_id"]], raw, indexed)
            for recording in recordings for raw in recording["results"]]
    return {"version": 1, "rows": rows}
