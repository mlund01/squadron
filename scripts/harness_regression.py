#!/usr/bin/env python3
"""Run or summarize the hermetic mission-harness contract suite.

The manifest binds each required invariant to a real runtime test. A renamed,
skipped, or unexecuted test is a failed gate, even if `go test` exits cleanly.
The report intentionally excludes test stdout and trace payloads.
"""

import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parent.parent
MANIFEST = ROOT / "evals" / "harness" / "contracts.json"
PACKAGES = ("./mission/runtime", "./mission")


def command_output(*command):
    result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, check=False)
    return result.stdout.strip() if result.returncode == 0 else "unknown"


def collect_results(stream):
    results = {}
    for line in stream:
        if not line.strip():
            continue
        event = json.loads(line)
        package = event.get("Package")
        name = event.get("Test")
        action = event.get("Action")
        if package and action in ("run", "pass", "fail", "skip") and (name or action == "fail"):
            results.setdefault((package, name or ""), []).append(action)
    return results


def evaluate(manifest, results, test_exit_code):
    cases = []
    for case in manifest["cases"]:
        actions = results.get((case["package"], case["test"]), [])
        if "fail" in actions:
            status = "fail"
        elif "skip" in actions:
            status = "skip"
        elif "run" in actions and "pass" in actions:
            status = "pass"
        else:
            status = "missing"
        cases.append({**case, "status": status})
    return cases, test_exit_code == 0 and all(case["status"] == "pass" for case in cases)


def write_report(output_dir, report):
    output_dir.mkdir(parents=True, exist_ok=True)
    (output_dir / "harness-contracts.json").write_text(json.dumps(report, indent=2) + "\n")
    lines = [
        "# Harness contract regression",
        "",
        f"Result: **{'PASS' if report['passed'] else 'FAIL'}**",
        f"Commit: `{report['commit']}`",
        f"Go: `{report['go_version']}`",
        f"Manifest version: `{report['manifest_version']}` (`{report['manifest_sha256']}`)",
        f"Model/prompt: `{report['model_provider']}` / `{report['prompt_version']}`",
        f"Test command: `{report['test_command']}`",
        f"Go test exit code: `{report['test_exit_code']}`",
        "",
        "| Contract | Result | Evidence test |",
        "| --- | --- | --- |",
    ]
    for case in report["cases"]:
        lines.append(f"| {case['id']} | {case['status']} | `{case['package']}.{case['test']}` |")
    if report["failed_tests"]:
        lines.extend(["", "Failed Go tests/packages:"])
        lines.extend(f"- `{name}`" for name in report["failed_tests"])
    lines.extend(["", "A missing or skipped evidence test fails the contract gate. Test stdout is not stored in this report.", ""])
    (output_dir / "harness-contracts.md").write_text("\n".join(lines))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, help="Existing `go test -json` output; omit to run the two mission packages")
    parser.add_argument("--test-exit-code", type=int, default=0, help="Exit code of the test command when --input is used")
    parser.add_argument("--test-command", default="external go test -json", help="Exact command that produced --input")
    parser.add_argument("--output-dir", type=Path, default=ROOT / "artifacts" / "harness-regression")
    args = parser.parse_args()
    manifest_bytes = MANIFEST.read_bytes()
    manifest = json.loads(manifest_bytes)
    if args.input:
        input_path = args.input
        test_exit_code = args.test_exit_code
        test_command = args.test_command
    else:
        with tempfile.NamedTemporaryFile(mode="w+", suffix=".jsonl") as stream:
            command = ["go", "test", "-json", *PACKAGES]
            test_exit_code = subprocess.run(command, cwd=ROOT, stdout=stream, check=False).returncode
            stream.flush()
            input_path = Path(stream.name)
            with input_path.open() as source:
                results = collect_results(source)
        input_path = None
        test_command = " ".join(command)
    if input_path is not None:
        with input_path.open() as source:
            results = collect_results(source)
    cases, passed = evaluate(manifest, results, test_exit_code)
    failed_tests = sorted(f"{package}.{name}".rstrip(".") for (package, name), actions in results.items() if "fail" in actions)
    report = {
        "schema_version": 1,
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "commit": command_output("git", "rev-parse", "HEAD"),
        "go_version": command_output("go", "version"),
        "manifest_version": manifest["version"],
        "manifest_sha256": hashlib.sha256(manifest_bytes).hexdigest(),
        "model_provider": "none (hermetic)",
        "prompt_version": "none (hermetic)",
        "test_command": test_command,
        "test_exit_code": test_exit_code,
        "passed": passed,
        "failed_tests": failed_tests,
        "cases": cases,
    }
    write_report(args.output_dir, report)
    print(f"Harness contracts: {sum(case['status'] == 'pass' for case in cases)}/{len(cases)} passed; report: {args.output_dir}")
    for name in failed_tests:
        print(f"Go test failed: {name}", file=sys.stderr)
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())
