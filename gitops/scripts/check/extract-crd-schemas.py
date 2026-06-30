#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
from collections.abc import Iterable
from pathlib import Path

import yaml


def load_documents(path: Path) -> list[dict]:
    with path.open(encoding="utf-8") as handle:
        return [doc for doc in yaml.safe_load_all(handle.read()) if isinstance(doc, dict)]


def crd_to_schema(crd: dict, version_entry: dict) -> dict:
    group = crd["spec"]["group"]
    kind = crd["spec"]["names"]["kind"]
    version = version_entry["name"]
    schema = version_entry.get("schema", {}).get("openAPIV3Schema", {"type": "object"})
    return {
        "type": "object",
        "properties": {
            "apiVersion": {"type": "string", "enum": [f"{group}/{version}"]},
            "kind": {"type": "string", "enum": [kind]},
            "metadata": {"type": "object"},
            "spec": schema.get("properties", {}).get("spec", {"type": "object"}),
            "status": schema.get("properties", {}).get("status", {"type": "object"}),
        },
        "required": ["apiVersion", "kind", "metadata"],
    }


def write_schemas_from_documents(documents: Iterable[dict], out_dir: Path) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)
    for doc in documents:
        if doc.get("kind") != "CustomResourceDefinition":
            continue
        for version_entry in doc.get("spec", {}).get("versions", []):
            if not version_entry.get("schema", {}).get("openAPIV3Schema"):
                continue
            kind = doc["spec"]["names"]["kind"]
            version = version_entry["name"]
            target = out_dir / f"{kind}_{version}.json"
            target.write_text(json.dumps(crd_to_schema(doc, version_entry), indent=2) + "\n")


def main() -> int:
    if len(sys.argv) < 3:
        print("usage: extract-crd-schemas.py <output-dir> <crd-file>...", file=sys.stderr)
        return 2
    out_dir = Path(sys.argv[1])
    for source in sys.argv[2:]:
        write_schemas_from_documents(load_documents(Path(source)), out_dir)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
