"""Run with PyYAML installed and kubeconform on PATH."""

import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "extract_crd_schemas",
    Path(__file__).parents[1] / "scripts/check/extract-crd-schemas.py",
)
extract = importlib.util.module_from_spec(spec)
spec.loader.exec_module(extract)

DURATION = {
    "type": "string",
    "format": "duration",
    "pattern": r"^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$",
}
CRD = {
    "kind": "CustomResourceDefinition",
    "spec": {
        "group": "example.org",
        "names": {"kind": "AlertGroup"},
        "versions": [{
            "name": "v1",
            "schema": {"openAPIV3Schema": {
                "type": "object",
                "properties": {"spec": {
                    "type": "object",
                    "required": ["interval"],
                    "properties": {
                        "interval": DURATION,
                        "rules": {"type": "array", "items": {
                            "type": "object",
                            "properties": {"keepFiringFor": DURATION},
                        }},
                        "timestamp": {"type": "string", "format": "date-time"},
                    },
                }},
            }},
        }],
    },
}


class SchemaTests(unittest.TestCase):
    def test_only_pattern_backed_duration_format_is_normalized(self):
        original = copy.deepcopy(CRD)
        normalized = extract.normalize_formats(CRD)
        self.assertEqual(CRD, original)
        self.assertNotEqual(normalized, CRD)
        for schema in (
            {"type": "string", "format": "duration"},
            {"type": "string", "format": "date-time", "pattern": ".+"},
        ):
            self.assertEqual(extract.normalize_formats(schema), schema)

    def test_kubeconform_resolves_extracted_schema_and_enforces_constraints(self):
        with tempfile.TemporaryDirectory() as directory:
            extract.write_schemas_from_documents([CRD], Path(directory))
            for fields, valid in (
                ({"interval": "1m"}, True),
                ({"interval": "1h30m0.5s", "rules": [{"keepFiringFor": "10m"}]}, True),
                ({"interval": "PT1M"}, False),
                ({"interval": "garbage"}, False),
                ({"interval": 60}, False),
                ({}, False),
                ({"interval": "1m", "rules": [{"keepFiringFor": "bad"}]}, False),
                ({"interval": "1m", "timestamp": "bad"}, False),
            ):
                with self.subTest(fields=fields):
                    manifest = {
                        "apiVersion": "example.org/v1", "kind": "AlertGroup",
                        "metadata": {"name": "test"}, "spec": fields,
                    }
                    result = subprocess.run(
                        ["kubeconform", "-schema-location",
                         directory + "/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"],
                        input=json.dumps(manifest), text=True, capture_output=True,
                    )
                    self.assertEqual(result.returncode == 0, valid, result.stdout + result.stderr)
                    if not valid:
                        self.assertIn("is invalid", result.stdout)


if __name__ == "__main__":
    unittest.main()
