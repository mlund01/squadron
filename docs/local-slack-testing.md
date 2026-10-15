# Test the Slack integration locally

Use the request recorder to inspect the messages the gateway sends, or run the
smoke mission to exercise questions and answers in a Slack test channel. For
building Squadron and testing mission execution with a local model stub, see
[Test Squadron locally](local-testing.md).

## Preview the outgoing calls without credentials

After setting up the sibling checkouts and Go workspace below, run from their
parent directory:

```bash
mkdir -p preview
SQUADRON_SLACK_PREVIEW="$PWD/preview/slack-calls.json" \
  go test ./squadron-gateway-slack -run '^TestMockSlackPreview$' -count=1 -v
```

The recorder uses local substitutes for Slack’s API and Squadron’s mission API.
It supplies mission events and answers directly to the gateway, so it needs no
credentials. It records two entry paths: a mission that posts first, and a mission
started through the slash-command form. Each sequence includes root updates,
replies, choice and free-text questions, answers, a second run using the same
request key, a failure notification, and a checkpoint reload.

The JSON contains `calls` with the API method, message payload, returned timestamp,
and scenario step, plus `checks` for shared-thread behavior. `chat.postMessage`
without `thread_ts` creates a root; with `thread_ts`, it creates a reply.
`chat.update` changes the message identified by `ts`. The payload's `blocks`
contain Slack's question controls, while `views.open` and `views.update` contain
the start form.

The recorder continues after a failed thread check so the output includes later
messages as well. Therefore, a successful test exit means the capture completed;
inspect `checks` for thread-mapping failures. The local server accepts the recorded
payloads without enforcing Slack’s validation rules. Use the live test below to
check Slack’s acceptance of the messages and delivery of user interactions.

## When are real credentials needed?

`verify` checks the fixture's configuration and finds the local gateway binary;
it does not authenticate to Slack or call a model. Placeholder values are enough
for that check. They are not enough to execute a mission.

A live `/squadron` form needs real Slack tokens but does not call the model until
you submit it. Running the fixture needs a real Anthropic API key and incurs
model usage. A dummy key will cause the run to fail rather than simulate an agent.

## Build the host and gateway

Use the sibling checkouts and SDK workspace override described in the
[local testing guide](local-testing.md#how-do-i-build-and-test-source-changes).
From their parent directory:

```bash
mkdir -p bin
go build -o bin/squadron ./squadron/cmd/cli
go build -o bin/slack-gateway ./squadron-gateway-slack
go test ./squadron-gateway-slack/...
(cd squadron-gateway-sdk && GOWORK=off go test ./...)
```

## Prepare the live smoke mission

The [smoke fixture](../examples/slack-smoke/squadron.hcl) posts a root message,
asks two questions, and routes to a second mission that joins the same thread.
Its tools are limited to posting messages and asking questions.

From the same parent directory:

```bash
mkdir -p smoke
cp squadron/examples/slack-smoke/squadron.hcl smoke/squadron.hcl
bin/squadron init -c smoke
platform="$(go env GOOS)-$(go env GOARCH)"
mkdir -p "smoke/.squadron/gateways/$platform/slack/local"
cp bin/slack-gateway "smoke/.squadron/gateways/$platform/slack/local/gateway"
```

`version = "local"` selects this binary instead of a release download. The cache
is inside the test configuration's `.squadron` directory, not `~/.squadron`.
Its vault, database, and Slack checkpoint therefore do not share production run
history or request-to-thread mappings. Do not set `SQUADRON_HOME` or pass a
`--squadron-home` override for this example; either changes where the state lives.

The fixture uses MCP port 18090. Its gateway's `mcp_url` and the host's `port`
must agree so the slash-command form can list and start missions. The same
`mcp_host_secret` authenticates that connection. Generate a random value and
store it in the test vault.

Populate these vault variables using `bin/squadron vars -c smoke set NAME VALUE`:

| Variable | Value |
| --- | --- |
| `anthropic_api_key` | Anthropic API key, or a placeholder for validation only |
| `slack_bot_token` | Test app's bot token (`xoxb-…`) |
| `slack_app_token` | Test app's Socket Mode token (`xapp-…`) |
| `slack_channel_id` | Dedicated test channel ID |
| `mcp_host_secret` | Random secret used only for this local MCP host |

To avoid putting a token in shell history, use a hidden prompt in Bash:

```bash
read -r -s -p 'Slack bot token: ' squadron_test_token
printf '\n'
bin/squadron vars -c smoke set slack_bot_token "$squadron_test_token"
unset squadron_test_token
```

Repeat for each credential. The CLI accepts values as arguments, so they may be
briefly visible to local process inspection. Keep the vault and its key out of
version control. The file vault provider keeps both in `.squadron`, which `init`
adds to the test directory's ignore file.

## Connect the test app

Follow the Slack gateway's README to enable Socket Mode, create `/squadron`,
install the bot with `chat:write` and `commands`, and give its app token
`connections:write`. Invite the bot to the test channel. Use a dedicated test app:
multiple instances connected with the same app token can receive events intended
for one another, so a separate channel alone does not isolate the consumer.

Then validate and start from the test directory:

```bash
bin/squadron verify smoke
cd smoke
../bin/squadron engage --foreground --headless -c .
```

The gateway checkpoint path is relative to the working directory, so start from
`smoke` as shown. Foreground mode keeps logs in the terminal, and Ctrl-C stops the
instance. Headless mode avoids downloading a command-center UI; Slack talks to
the local MCP host directly. Wait for the gateway’s ready message in the log before opening the form, because
`verify` checks configuration without connecting to Slack.

## Check the behavior in Slack

1. Run `/squadron` and choose `slack_smoke`. `case_key` should be required;
   `states` and `fail_after_reply` should show defaults; `hidden_marker` should
   not appear. Enter malformed JSON for `states` and confirm the form reports an
   input error without starting a run.
2. Submit a fresh key such as `SLACK-TEST-001`, leaving failure disabled. Expect
   one top-level message, a synthetic reply, a choice question, and a free-text
   question with an Answer button. Answer both. The question messages should
   update to show their answers.
3. The mission routes to `slack_smoke_finish` with the same key. Its first post
   should update the existing root, and its question should stay in that thread.
   Answer it and check that the root shows completion. The requester line should
   survive root updates, while replies should not repeatedly mention the user.
4. Run a fresh case with `fail_after_reply` set to JSON `true`. After the two
   answers, expect an intentional failure notification in the case's thread.
5. Stop and restart the instance, then start `slack_smoke_finish` with a previous
   case key. It should rejoin the saved thread. Use a fresh key for unrelated
   tests; reusing a key deliberately exercises the persisted mapping.

If a routed run opens a second root, inspect whether the gateway saved both the
run-to-thread and key-to-thread mappings when the first run posted its root.
If an agent skips a requested step, inspect its tool calls before attributing the
result to Slack transport: this fixture uses a live model, not a scripted one.
