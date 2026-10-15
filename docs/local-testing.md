# Test Squadron locally

Run Squadron against a local model stub to reproduce mission behavior without
model credentials. The supplied scenarios execute the CLI and check its saved
results. Each run gets its own configuration, vault, database, and logs so tests
can be repeated without reusing an earlier run’s state.

## How do I run a mission without credentials?

From the Squadron checkout, with Go and Python 3 installed:

```bash
make run
```

This builds the CLI and executes four scenarios through that binary:

| Scenario | What the real runtime must do |
| --- | --- |
| `routing` | Resolve an input, complete classification, run the selected repair task, and leave the other branch unexecuted |
| `delegation` | Call a worker agent, receive its answer, and complete the commander's task |
| `failure` | Return a failure and persist the failed mission and task |
| `missing-input` | Reject a missing required input before calling the model or starting a mission |

The model endpoint is a loopback HTTP server that returns scripted Anthropic
responses, including streamed tool calls. Squadron still parses HCL, builds its
prompts and tools, sends model requests, executes tool calls, and writes its real
SQLite state. The script checks the exit result, number of model requests, and
persisted mission and task states. Unexpected requests or an exhausted script
fail the run, so a missing response cannot silently become a success.

The fixture points the model at the local stub, which accepts its placeholder
API key. To test model decisions instead of scripted responses, use a provider
endpoint and a valid key in a separate test configuration.

## Where are the results, and how do I repeat a test?

The default output directory is `.local-testing` inside the checkout. It is
ignored by Git. Set `SQUADRON_TEST_ROOT` to use a separate workspace:

```bash
export SQUADRON_TEST_ROOT="$HOME/code/squadron-local-testing"
make run SCENARIOS=routing
make run SCENARIOS="delegation failure missing-input"
```

Each invocation creates a fresh directory under `runs`, so a previous database
cannot make a test appear to pass. Its `result` directory contains:

- `result.json`: checks and observed mission/task states;
- `mission.log`: CLI output;
- `model-requests.json`: the actual prompts, tools, and conversation sent to the stub;
- `scenario.json`: the scripted decisions and expected outcomes;
- `config/`: the rendered HCL and `.squadron` vault and database.

Keep this output local: when adapting a fixture, prompts and logs may contain the
data you supplied. To inspect a failure, start with `result.json`, then compare
the log and model requests against the script. A scripted-model success establishes
runtime behavior for those decisions, not whether a live model would choose them.

## How do I build and test source changes?

```bash
make build
make test
```

`build` writes the CLI to the test root's `bin` directory. `test` rebuilds it,
runs the configuration, mission, agent, built-in tool, storage, human-input,
gateway-host, and MCP-host package tests, then runs the four CLI scenarios.
Run `go test ./...` for the complete repository suite.

For related repositories, check out the intended branches in sibling directories.
A Go workspace can make the runtime and an integration use the same SDK source
without editing their published module dependencies. For the Slack gateway,
run from the sibling checkouts' parent directory:

```bash
go work init ./squadron ./squadron-gateway-slack
go work edit -replace=github.com/mlund01/squadron-gateway-sdk=./squadron-gateway-sdk
```

Create the workspace once; update an existing workspace instead of running init
again. The SDK is the replacement target, so do not also add it to the `use` list.
Other integrations need their own module paths and dependency overrides.

After updating a branch, rebuild the affected binaries and restart any running
instance. A running process does not adopt a newly built binary automatically.
Record `git rev-parse HEAD` for each participating checkout when sharing results;
include local diffs if those checkouts have uncommitted changes.

## How do I add a scenario?

The runnable fixtures live in `examples/local-testing`. A scenario JSON selects
a mission, supplies its inputs, scripts model turns, and states the expected
outcome. Each turn matches an expected prompt marker and returns either a tool
call or text. The adjacent `squadron.hcl` supplies the configuration; the runner
replaces `__MODEL_URL__` with its temporary loopback endpoint.

To run a fixture from another directory:

```bash
python3 scripts/local-test.py /path/to/fixture/scenario.json \
  --binary .local-testing/bin/squadron \
  --output .local-testing/runs/my-scenario
```

Use an output path that does not exist. The runner refuses to overwrite evidence.
The fixture must be self-contained in the adjacent `squadron.hcl`, because the
runner copies only that file into the run directory. The ordered response
script suits sequential scenarios;
parallel agent calls require matching responses by request identity instead of
assuming a single call order.

Choose assertions that establish the behavior under test. For example, a routing
scenario should verify which branch executed and which did not, rather than only
checking that the model returned a route string. To test tool payloads, provide a
local substitute for that tool's service and inspect the requests it receives.

## How do I run my own configuration interactively?

Create a disposable configuration directory from the revision you want to test,
then run:

```bash
make verify CONFIG=/path/to/test-config
make start CONFIG=/path/to/test-config
```

The instance runs in the foreground; Ctrl-C stops it. Both commands explicitly use
that configuration's `.squadron` directory, so an inherited `SQUADRON_HOME` cannot
redirect them into another instance's state. `start` initializes its vault if needed
and runs headless. To use the command center instead, run the built CLI with
`engage --foreground --init -c .` from the configuration directory and choose an
unused `--cc-port`.

Review the copied configuration before loading it. `verify` can install plugins
and contact configured MCP services, while `start` can start gateways and schedules.
An isolated database does not isolate external side effects. Point model and
service endpoints at local stubs, replace external tools with test implementations,
and remove automatic triggers or schedules that are outside the scenario. The
commands load the configuration as written; they do not substitute endpoints
or credentials.

For a live-model test, store the model key in that test directory's vault and use
the provider's endpoint. For a mixed test, use a real model with stubbed service
calls, or a scripted model with a selected real integration. Document which parts
are real so captured output is not mistaken for evidence about a stubbed component.

## How do integration-specific tests fit in?

Build and install a local integration only when the scenario needs it. A gateway
binary belongs at
`<test-config>/.squadron/gateways/<GOOS>-<GOARCH>/<name>/local/gateway`, with
`version = "local"` in the test configuration. Keep relative checkpoint paths
inside that configuration and start from its directory so saved mappings stay
with the same test state.

[Slack message testing](local-slack-testing.md) shows how to capture a gateway’s
outgoing API requests with a local server and how to connect a test instance to
Slack. Choose the test boundary according to the behavior you need to check:
supply events directly to inspect gateway formatting, or run a mission to check
how its tool calls produce those events.
