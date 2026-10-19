"""Contract tests for lossless schema scoring; no retriever or network needed."""
import copy
import json
from pathlib import Path
import unittest

from scoring import field_hit, score_case, schema_bytes, summarize

ROOT = Path(__file__).parent
CATALOG = {t["name"]: t for t in json.loads((ROOT / "testdata/catalog.json").read_text())["tools"]}
CASES = json.loads((ROOT / "testdata/cases.json").read_text())["cases"]


class ScoringTest(unittest.TestCase):
    def test_all_frozen_pointers_exist(self):
        for case in CASES:
            for route in case["routes"]:
                tool = CATALOG[route["tool"]]
                for side in ("input", "output"):
                    schema = tool[side + "Schema"]
                    for pointer in route[side + "_fields"]:
                        self.assertTrue(field_hit(schema, schema, pointer), (case["id"], pointer))

    def test_missing_field_or_tool_is_miss(self):
        source = CATALOG["summarize_text"]["inputSchema"]
        self.assertFalse(field_hit(source, {"type": "object"}, "/properties/text"))
        result = score_case(CASES[0], [], CATALOG)
        self.assertFalse(result["tool_hit"])
        self.assertEqual(result["routes"][0]["input_hits"], 0)

    def test_annotations_ignored_but_contract_constraints_preserved(self):
        source = CATALOG["search"]["inputSchema"]
        target = copy.deepcopy(source)
        target["properties"]["limit"]["description"] = "unrelated annotation"
        self.assertTrue(field_hit(source, target, "/properties/limit"))
        target["properties"]["limit"]["maximum"] = 201
        self.assertFalse(field_hit(source, target, "/properties/limit"))
        target = copy.deepcopy(source)
        target["required"] = []
        self.assertFalse(field_hit(source, target, "/properties/query"))

    def test_nested_required_membership_is_preserved(self):
        source = CATALOG["generate_dpi_spec"]["outputSchema"]
        target = copy.deepcopy(source)
        target["properties"]["viewport"]["required"] = []
        self.assertFalse(field_hit(source, target, "/properties/viewport/properties/width"))

    def test_or_route_does_not_merge_partial_fields(self):
        case = next(c for c in CASES if c["id"] == "ambiguous-search")
        first = copy.deepcopy(CATALOG["search"])
        second = copy.deepcopy(CATALOG["search_records"])
        del first["inputSchema"]["properties"]["limit"]
        del second["inputSchema"]["properties"]["query"]
        result = score_case(case, [first, second], CATALOG)
        self.assertTrue(result["tool_hit"])
        self.assertFalse(result["complete_route"])
        self.assertLess(result["best_route_field_recall"], 1)

    def test_unsupported_field_separate_from_supported_denominator(self):
        case = next(c for c in CASES if c["id"] == "unsupported-field")
        tool = copy.deepcopy(CATALOG["summarize_text"])
        tool["outputSchema"]["properties"]["exchange_rate"] = {"type": "number"}
        result = score_case(case, [tool], CATALOG)
        self.assertTrue(result["complete_route"])
        self.assertTrue(result["unsupported_field_exposed"])
        self.assertEqual(result["routes"][0]["output_total"], 1)

    def test_variant_weighting_preserves_misses(self):
        hit = {"tools": [CATALOG["summarize_text"]]}
        miss = {"tools": []}
        recording = {"arm": "baseline", "results": [{"case_id": CASES[0]["id"], "k": 1,
            "variants": [{"payload": hit, "native_payload_bytes": 10}, {"payload": miss, "native_payload_bytes": 2}],
            "sample_variants": [0, 1, 1], "warmup_variants": [0], "latency_ns": [10, 20, 30]}]}
        row = summarize([CASES[0]], {"tools": list(CATALOG.values())}, [recording])["rows"][0]
        self.assertEqual(row["tool_hit_rate"], 1 / 3)
        self.assertEqual(row["complete_route_rate"], 1 / 3)
        self.assertEqual(row["shortlist_size_range"], [0, 1])
        self.assertEqual([variant["samples"] for variant in row["variants"]], [1, 2])
        self.assertEqual(row["latency"]["count"], 3)

    def test_schema_bytes_preserve_integer_bounds(self):
        tool = CATALOG["projected_lookup_tool"]
        expected = json.dumps({"inputSchema": tool["inputSchema"], "outputSchema": tool["outputSchema"]}, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        self.assertIn(b"18446744073709551615", expected)
        self.assertEqual(schema_bytes([tool]), len(expected))


if __name__ == "__main__":
    unittest.main()
