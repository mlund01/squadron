#!/usr/bin/env python3
"""Run a Squadron mission with a scripted local model and inspect persisted results."""
import argparse
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("scenario", type=Path)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    scenario_path = args.scenario.resolve()
    spec = json.loads(scenario_path.read_text())
    binary = str(args.binary.resolve())
    output = args.output.resolve()
    # Refusing reuse preserves the database and transcript of an earlier run.
    output.mkdir(parents=True, exist_ok=False)
    config_dir = output / "config"
    config_dir.mkdir()
    calls, errors = [], []
    turns = spec["turns"]
    lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            with lock:
                index = len(calls)
                calls.append(request)
                turn = turns[index] if index < len(turns) else None
                error = None
                if self.path.split("?")[0] != "/v1/messages":
                    error = "Unexpected model endpoint: " + self.path
                elif turn is None:
                    error = "Script exhausted; refusing to fabricate more model responses"
                elif turn["expect"] not in json.dumps(request):
                    error = "Request did not contain expected marker: " + turn["expect"]
                elif "tool" in turn and turn["tool"] not in [t["name"] for t in request.get("tools", [])]:
                    error = "Requested tool is not available: " + turn["tool"]
                if error:
                    errors.append(error)
            if error:
                self.send_response(400)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"type": "error", "error": {"type": "invalid_request_error", "message": error}}).encode())
                return
            block = ({"type": "tool_use", "id": "local_" + str(index), "name": turn["tool"], "input": turn["input"]}
                     if "tool" in turn else {"type": "text", "text": turn["text"]})
            stop_reason = "tool_use" if "tool" in turn else "end_turn"
            message = {"id": "msg_local_" + str(index), "type": "message", "role": "assistant", "model": request["model"], "content": [block], "stop_reason": stop_reason, "stop_sequence": None, "usage": {"input_tokens": 1, "output_tokens": 1}}
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream" if request.get("stream") else "application/json")
            self.end_headers()
            if not request.get("stream"):
                self.wfile.write(json.dumps(message).encode())
                return
            def event(kind, **fields):
                self.wfile.write(("event: " + kind + "\ndata: " + json.dumps({"type": kind, **fields}) + "\n\n").encode())
            event("message_start", message={**message, "content": [], "stop_reason": None})
            if "tool" in turn:
                event("content_block_start", index=0, content_block={**block, "input": {}})
                event("content_block_delta", index=0, delta={"type": "input_json_delta", "partial_json": json.dumps(block["input"])})
            else:
                event("content_block_start", index=0, content_block={"type": "text", "text": ""})
                event("content_block_delta", index=0, delta={"type": "text_delta", "text": block["text"]})
            event("content_block_stop", index=0)
            event("message_delta", delta={"stop_reason": stop_reason, "stop_sequence": None}, usage={"output_tokens": 1})
            event("message_stop")

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    model_url = "http://127.0.0.1:" + str(server.server_port)
    template = scenario_path.parent / "squadron.hcl"
    (config_dir / "squadron.hcl").write_text(template.read_text().replace("__MODEL_URL__", model_url))
    shutil.copy2(scenario_path, output / "scenario.json")
    env = dict(os.environ)
    env.pop("SQUADRON_HOME", None)
    env.pop("SQUADRON_FORKED", None)
    command = [binary, "--squadron-home", str(config_dir / ".squadron"), "mission", spec["mission"], "-c", str(config_dir), "--init"]
    for key, value in spec.get("inputs", {}).items():
        command += ["--input", key + "=" + (value if isinstance(value, str) else json.dumps(value))]
    result = None
    try:
        with (output / "mission.log").open("w") as log:
            result = subprocess.run(command, cwd=config_dir, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=45)
    except subprocess.TimeoutExpired:
        errors.append("Mission exceeded the 45-second test timeout")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
        (output / "model-requests.json").write_text(json.dumps(calls, indent=2) + "\n")
    if result is not None and result.returncode != spec["expected_exit"]:
        errors.append(f"Expected exit {spec['expected_exit']}, got {result.returncode}")
    if len(calls) != len(turns):
        errors.append(f"Expected {len(turns)} model calls, got {len(calls)}")
    if spec.get("expected_log", "") not in (output / "mission.log").read_text():
        errors.append("Expected error text missing from mission log")
    db_path = config_dir / ".squadron" / "store.db"
    missions, tasks = [], []
    if db_path.exists():
        with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as db:
            db.row_factory = sqlite3.Row
            missions = [dict(row) for row in db.execute("SELECT id, mission_name, status, input_values_json FROM missions")]
            tasks = [dict(row) for row in db.execute("SELECT task_name, status, summary, error FROM mission_tasks")]
    if len(missions) != spec.get("expected_mission_count", 1):
        errors.append(f"Unexpected persisted mission count: {len(missions)}")
    if "expected_mission_status" in spec and (not missions or missions[0]["status"] != spec["expected_mission_status"]):
        errors.append("Persisted mission status did not match")
    by_name = {task["task_name"]: task["status"] for task in tasks}
    for name, status in spec.get("expected_tasks", {}).items():
        if by_name.get(name) != status:
            errors.append(f"Task {name}: expected {status}, got {by_name.get(name)}")
    for name in spec.get("must_not_complete", []):
        if by_name.get(name) == "completed":
            errors.append("Unselected task completed: " + name)
    report = {"passed": not errors, "errors": errors, "model_calls": len(calls), "missions": missions, "tasks": tasks}
    (output / "result.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))
    print("Evidence: " + str(output))
    return bool(errors)


if __name__ == "__main__":
    raise SystemExit(main())
