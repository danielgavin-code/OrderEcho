# REPORT_A4 — OrderEcho Go agent, Cook A4

A4 is built end to end:
- the **agent service** (`orderecho serve`): loopback only; owns every session, order manager, cert run and attestation; JSON API under `/api/v1`;
- the **MCP server**: stdio (`orderecho mcp`, which starts the service in the background when none is running) and HTTP (`serve --mcp-http`, bearer token required). It has 20 tools, built on the official Go SDK (`github.com/modelcontextprotocol/go-sdk` **v1.8.0**, pinned);
- the **Claude Desktop helper** (`orderecho mcp install-claude-desktop [--write]`);
- the §7 **safety** rules and the §3 **carry-overs**: 9.1 is now auto, and 9.2 is assisted with a drafted deviations section.

Version is `0.4.0` / build `a4`.

`make test`, `make lint` and `make interop` all pass:
- **Unit: 165 PASS, 0 FAIL.** That is the 143 from A3 plus 22 new.
- **Interop: 34 PASS, 0 FAIL, 0 SKIP** (178 s), all against the real emulator. This includes the 5 new MCP tests, which drive a real Go-SDK MCP client.
- Parity is unchanged: 81/81 cases agree, plus 7 documented divergences.

The SDK did everything §5 needs, so no protocol code was hand-rolled.

**Your emulator was running again** on the standard ports (pid 4282, `orderecho_multi.yaml`). Following A3 question 6, I left it alone. The smoke tests and the §5 real run used **my own emulator copy on other ports** (FIX 19878/19879, control API 18090, service 18190), with all its data in my scratch directory. It is stopped now, and so is everything else I started. Your emulator is still running and got none of my traffic.

## 1. Files

New:
```
internal/service/service.go            the service: sessions (one fresh agent per connect), Dispatch (validate -> handler ->
                                       engine-log "MCP  <tool> <args> -> <result>" + evidence event), shutdown, run ids
internal/service/tools.go              the tool catalogue: 20 tools, input structs -> JSON Schemas (enums, types), LLM-facing
                                       descriptions, groups (core / orders / emulator), annotations
internal/service/handlers_session.go   list_sessions, connect/disconnect_session, session_status, recent_messages (decoded)
internal/service/handlers_orders.go    send/cancel/replace_order (wait_for none|ack|terminal), list_orders, order_timeline
internal/service/handlers_cert.go      list_cert_suites/targets, start_cert_run (async), cert_run_status/results,
                                       attest_cert_case, run.json persistence, restart recovery
internal/service/handlers_emulator.go  emulator_fill/cancel/hold_order, emulator_inject_next (the emulator's control API)
internal/service/http.go               /api/v1 routes, loopback Host/Origin guard, JSON-only POSTs; the HTTP client the
                                       stdio MCP server uses
internal/service/ring.go               last 2000 wire messages per session, for recent_messages / session_status
internal/service/service_test.go       unit tests (no sockets)
internal/mcpserver/mcpserver.go        MCP server over the SDK: catalogue -> tools with annotations; structured content
                                       + plain-text summary; errors as isError with {error, detail, hint}; HTTP handler
                                       behind the SDK's RequireBearerToken
internal/mcpserver/mcpserver_test.go   unit tests (in-memory MCP transport, forwarding, bearer token)
internal/cert/review.go                9.1 / 9.2 reviews (Finalize), deviations draft + deviations.md, Attest (after a run)
internal/cert/review_test.go           unit tests for 9.1 auto, 9.2 draft, attestation after the run
internal/cert/execute.go               cert.Execute: one live cert run (fresh agent, reset, logout, results), shared by
                                       the CLI and the service
internal/fix/profile/names.go          FIX tag names and enum value names for decoded messages
cmd/orderecho/serve.go                 orderecho serve [--mcp-http] [--port N] | serve --status
cmd/orderecho/mcpcmd.go                orderecho mcp (stdio, auto-start) | mcp install-claude-desktop [--write]
cmd/orderecho/a4_test.go               CLI tests: token refusal, loopback-only host, --status, Claude Desktop merge
internal/config/a4_test.go             config tests for service / mcp / emulator
internal/interop/mcp_test.go           A4 interop 1-7
REPORT_A4.md
```
Changed:
```
internal/version/version.go      0.4.0 / a4
certs/order_entry_fix42.yaml     9.1 -> mode auto, step "review: required_cases"; 9.2 -> mode assisted, "review: deviations"
                                 (task texts untouched; the drift test still passes; 4.4 inherits via extends)
internal/cert/suite.go           new step type "review" (required_cases | deviations) + its validation
internal/cert/runner.go          reviews decided after all other cases; OnCaseStart / Abort hooks; Tally; results.json gains
                                 attestable, review, required_in_suite, deviations, run_error, never_logged_on,
                                 session_dead, logout_timeout; ExitCode counts a run_error as ERROR (8)
internal/cert/results.go         deviations.md; "run: ERROR" summary line; ProgressLine
internal/cert/driver.go          Live.LogoutWith(text)
cmd/orderecho/cert.go            cert run now goes through cert.Execute (same output, same exit codes)
cmd/orderecho/main.go            serve and mcp commands, usage, ORDERECHO_HOME
internal/config/config.go        service {host, port}, mcp {allow_orders, allow_emulator_tools, http_token},
                                 emulator {control_api, sessions}; Session.External(); IsLoopbackHost
config/orderecho.yaml            the three new blocks (service 8190, emulator 8090 with the emu42/emu44/strict map)
internal/interop/cert_test.go    A3 checks follow the A4 rule for 9.1/9.2 (see §6.5), plus new assertions on both
go.mod / go.sum                  + github.com/modelcontextprotocol/go-sdk v1.8.0, github.com/google/jsonschema-go v0.4.3
```
Untouched: `../OrderEchoFixEmulator`, `certs/source/*.html`, and the repo's `data/` and `logs/`. Every test and run used temp or scratch directories.

## 2. Test outputs

### `make test`: 165 PASS, 0 FAIL

Unit tests by package:

| Package | Tests |
|---|---|
| cmd/orderecho | 4 (new) |
| agent | 1 |
| cert | 20 (+4) |
| checks | 32 |
| config | 5 (+1) |
| evidence | 2 |
| codec | 12 |
| session | 52 |
| logs | 5 |
| mcpserver | 4 (new) |
| order | 15 |
| service | 9 (new) |
| store | 4 |

```
go test -count=1 ./...
ok  	github.com/danielgavin-code/OrderEcho/cmd/orderecho	0.991s
ok  	github.com/danielgavin-code/OrderEcho/internal/agent	0.620s
ok  	github.com/danielgavin-code/OrderEcho/internal/cert	0.393s
ok  	github.com/danielgavin-code/OrderEcho/internal/checks	1.256s
?   	github.com/danielgavin-code/OrderEcho/internal/clock	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/config	1.526s
ok  	github.com/danielgavin-code/OrderEcho/internal/evidence	1.727s
ok  	github.com/danielgavin-code/OrderEcho/internal/fix/codec	1.731s
?   	github.com/danielgavin-code/OrderEcho/internal/fix/profile	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/fix/session	1.790s
?   	github.com/danielgavin-code/OrderEcho/internal/fix/transport	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/logs	1.691s
ok  	github.com/danielgavin-code/OrderEcho/internal/mcpserver	1.813s
ok  	github.com/danielgavin-code/OrderEcho/internal/order	1.770s
ok  	github.com/danielgavin-code/OrderEcho/internal/service	1.902s
ok  	github.com/danielgavin-code/OrderEcho/internal/store	1.929s
?   	github.com/danielgavin-code/OrderEcho/internal/version	[no test files]
```
The §8 unit list maps onto these tests:

| §8 unit item | Test(s) |
|---|---|
| Tool schemas validate | `TestToolSchemasValidate`: all 20 resolve as `type: object`; unknown arguments refused; every property has a description; valid and invalid `send_order` / `attest_cert_case` samples. |
| Argument validation and hints | `TestArgumentValidationAndHints`: 15 refusals, each with its code and the hint's content; `TestToolsOverMCPWithAnnotations` shows the hint reaching the model as `isError` text. |
| `last` resolution | `TestLastResolution`: across connections, newest first; an order-less newest connection; ClOrdID and OrderID lookups; `order_timeline` with and without `cl_ord_id`. |
| Annotations | `TestToolsOverMCPWithAnnotations`: read-only vs side-effect tools, destructive hints, and the safety wording in the descriptions, all over a real MCP session. |
| External-session confirm | `TestExternalSessionNeedsConfirmation`: send, cancel, replace and `start_cert_run`; loopback-name table. |
| Attestation without `user_confirmed` rejected | `TestAttestationNeedsUserConfirmed` (missing, false, non-attestable case, then accepted with results rewritten and audited) and the MCP-level check. |
| Token required on HTTP | `TestHTTPTransportRequiresBearerToken`: none, wrong, Basic and prefix tokens all get 401; the right token gets 200 and a real client works. `TestServeMCPHTTPRefusesWithoutToken`: exit 2. |
| 9.1 auto and 9.2 draft | `TestReviewCasesValidation`, `TestRequiredCasesReviewAndDeviationsDraft`, `TestDeviationsGroupWarnings`, `TestAttestAfterTheRun`. |

There are also tests for the config gates (`TestConfigGatesTools`, `TestConfigSwitchesToolsOff`), the HTTP shapes and guard, the call logging (`TestEveryCallIsLogged`), restart recovery, stdio forwarding over the HTTP API, and the Claude Desktop merge. `go test -race` is clean on service, mcpserver and cert.

### `make lint`
```
go vet ./...
go vet -tags interop ./...
```

### `make interop`: 34 PASS, 0 FAIL, 0 SKIP (178.5 s)
```
--- PASS: TestCertEmulatorFIX42 (57.42s)            --- PASS: TestMCPStdioOrdersAndEmulatorTools (5.09s)
--- PASS: TestCertEmulatorFIX44 (31.82s)            --- PASS: TestMCPCertRunMatchesCLI (5.79s)
--- PASS: TestCertStrictBrokerNegativeControl       --- PASS: TestMCPOverHTTPNeedsToken (2.03s)
--- PASS: TestCertGenericTargetBlocksAssisted       --- PASS: TestMCPServiceRestartMidRun (2.97s)
--- PASS: TestCertBrokenCaseAndError                --- PASS: TestMCPScriptedOperatorConversation (3.78s)
--- PASS: TestCLIOrderExitCodes, TestCLISessionPipedAndTimeline, TestScenario1..8, TestCtrlCLogsOutCleanly,
          TestA2Scenario01..11, TestParityTampered, TestParityKnownDivergences
ok  	github.com/danielgavin-code/OrderEcho/internal/interop	178.509s

PARITY SUMMARY: 81 case(s) compared, 81 agree, 0 disagree, 7 documented divergence(s) as expected
  A3-1 cert FIX 4.2 emulator (no attest)       exit 7  PASS 46, PENDING 11, N/A 11
  A3-1 cert FIX 4.2 emulator (attested)        exit 0  PASS 54, N/A 14
  A3-2 cert FIX 4.4 emulator                   exit 7  PASS 46, PENDING 11, N/A 11
  A3-3 cert strict broker (negative control)   exit 5  PASS 24, FAIL 23, PENDING 10, N/A 11
  A3-4 cert generic target (assisted BLOCKED)  exit 7  PASS 1, BLOCKED 8
  A3-5 broken case (FAIL + reason)             exit 5  step 3 (expect): timed out after 3s waiting for ord_status=FILLED ...
  A3-5 emulator killed mid-case                exit 3  B.2/B.3 ERROR (session dropped)
  A4-1..3 MCP stdio orders + emulator tools    PASS
  A4-4 cert via MCP == CLI; attestation        ORD PASS 5, N/A 4; attest exit 7 -> 0
  A4-5 MCP over HTTP (bearer token)            no token 401, bad token 401, token OK
  A4-6 service restart mid-run                 killed -> ERROR: service restarted; SIGTERM -> ERROR, logout; finished run readable
  A4-7 scripted operator conversation          emu44 FILLED PASS; ORD PASS 5, N/A 4 exit 0
```
Every A4 interop test uses the real emulator (temp storage, random ports) and a real `mcp.Client` from the Go SDK. Over stdio, the client spawns `orderecho mcp`, and that process starts the service itself.

1. **stdio** (`TestMCPStdioOrdersAndEmulatorTools`, subtest 1):
   - checks that no service was running first, then that `orderecho mcp` started one, with its output under `logs/engine/`;
   - `list_sessions`, `connect_session emu42`;
   - `send_order AAPL 100 buy mkt wait_for=terminal` → FILLED, verdict PASS, and each of the 11 checks is PASS;
   - `order_timeline last` → `D -> 8(New) -> 8(Fill)` with explanations;
   - `recent_messages`: the last message is an inbound ExecutionReport whose `OrdStatus` field carries `value 2, meaning Filled`; the `direction` / `msg_types` filter also works.
2. **Order flow** (subtest 2):
   - `emulator_inject_next` alters the emulator's next 35=8, and the ack carries the altered 58;
   - `send_order ZWZZT 1000 lmt 10.00 wait_for=ack` → NEW;
   - `emulator_fill_order` 100 @ 10.00, then 300 @ 9.95;
   - `order_timeline` gives **AvgPx 9.9625 both expected and reported (exact)**, Cum 400, Leaves 600, `avg_px` PASS;
   - `cancel_order last` → CANCELED, cum 400, leaves 0, verdict PASS;
   - a fill on the closed order → `emulator_order_closed`, with a hint.
3. **Replace flow** (subtest 3):
   - ZWZZT rests; `emulator_hold_order`;
   - `replace_order last qty=800 price=10.50` → Replaced;
   - the timeline chain is exactly `D 8 G 8`; the G has 38=800, 44=10.50 and 41 = the D; all checks PASS;
   - then cancel, `list_orders open` is empty, and `disconnect_session` logs out cleanly.
4. **Cert via tools** (`TestMCPCertRunMatchesCLI`):
   - `start_cert_run order-entry-fix42 emulator emu42 section=ORD`; order tools on emu42 are refused (`session_busy`) while it runs;
   - **the MCP client is closed mid-run and a new one attaches**: the run carried on;
   - poll to finished; `cert_run_results` gives PASS 5, N/A 4, exit 0;
   - **the same section through the CLI gives identical counts, case for case**;
   - a run of cases 1.1 + 4.1 → exit 7. `attest_cert_case 1.1`:
     - without `user_confirmed` → `confirmation_required`, and the run is unchanged;
     - on auto case 4.1 → `not_attestable`;
     - with `user_confirmed: true` → PASS and exit 0, with `results.json` on disk updated.
5. **HTTP MCP** (`TestMCPOverHTTPNeedsToken`):
   - `serve --mcp-http` without a token exits 2;
   - with `ORDERECHO_MCP_TOKEN`: `/mcp` without a token → 401, with a wrong token → 401;
   - an SDK streamable-HTTP client with the token runs `list_sessions`, `connect_session emu44`, `send_order` (FILLED, PASS), `order_timeline` and `disconnect_session`;
   - the engine log has the exact `MCP  send_order ... -> sent: ...` line, and the service evidence has the tool call.
6. **Restart** (`TestMCPServiceRestartMidRun`):
   - a finished 1-case run;
   - a full run is **SIGKILLed** after 3+ cases. The next tool call finds no service, so `orderecho mcp` starts a new one. That run then reads `error` / **`ERROR: service restarted`**. The finished run is still readable (exit 0), and sessions start DISCONNECTED;
   - then a full run with a **SIGTERM** mid-run: the session logs out cleanly (`58=OrderEcho agent service stopping` in the FIX log), and the run is ERROR ("service stopped while the run was in progress"). Its results.json has `run_error`, exit 8, and the unstarted cases are NOT_RUN with that reason.
7. **Scripted operator conversation** (`TestMCPScriptedOperatorConversation`): see §5. The same sequence is asserted step by step, with FIX 4.4 on emu44 and HeartBtInt 10.

## 3. Tools, as the LLM sees them

They are listed in catalogue order below; `tools/list` returns them sorted by name.
- **Arguments:** `?` marks an optional argument.
- **Kind** reflects the MCP annotations:
  - RO: `readOnlyHint: true`;
  - SE: side effects (`readOnlyHint: false`); "destructive" adds `destructiveHint: true`;
  - "open-world": talks to the counterparty or the emulator.

Each description opens with the sentence shown. The full text also says when to use the tool, what comes back and the common mistakes (§5).

| Tool | Arguments | Kind | Description (first sentence) |
|---|---|---|---|
| `list_sessions` | — | RO | Lists every FIX session configured in this OrderEcho agent: id, FIX version, route (SenderCompID -> TargetCompID), address, connection state, and whether it is external (a counterparty off this machine) or backed by the emulator. |
| `connect_session` | session_id, reset? | SE, open-world | Opens the TCP connection and logs on (35=A) for a configured session, and keeps it up between tool calls. |
| `disconnect_session` | session_id | SE, destructive, open-world | Logs out cleanly (35=5 both ways) and closes the connection. |
| `session_status` | session_id | RO | Detailed state of one session: connection state, sequence numbers (next out / next in), heartbeat interval and when a message last went each way, open orders, and a one-line summary of the last few messages. |
| `send_order` | session_id, symbol, side, qty, ord_type, price?, tif?, wait_for?, timeout_sec?, confirm_external? | SE, open-world | Sends a NewOrderSingle (35=D) on a connected session and, by default, waits for its first answer. |
| `cancel_order` | session_id, cl_ord_id (or "last"), wait_for?, timeout_sec?, confirm_external? | SE, destructive, open-world | Sends an OrderCancelRequest (35=F) for a working order, referring to it by any of its ClOrdIDs, its OrderID, or "last". |
| `replace_order` | session_id, cl_ord_id (or "last"), qty?, price?, wait_for?, timeout_sec?, confirm_external? | SE, destructive, open-world | Sends an OrderCancelReplaceRequest (35=G) changing a working order's total quantity and/or limit price; give at least one of qty or price. |
| `list_orders` | session_id, status? | RO | Lists the orders sent on a session (all its connections since the service started), newest last: ClOrdID, OrderID, symbol, side, quantity, type, price, state, cumulative/leaves quantity, average price and the checks verdict. |
| `order_timeline` | session_id, cl_ord_id? (or "last"), order_id? | RO | The full chain of one order: every message both ways (D, F, G, execution reports, rejects) with decoded ExecType/OrdStatus, quantities and prices, then the 11 order checks, each with PASS/WARN/FAIL and a plain-English explanation, and the overall verdict. |
| `recent_messages` | session_id, limit?, direction?, msg_types? | RO | The most recent FIX messages on a session (both directions by default), decoded: message type names, every field with its tag name and, for enumerations, the value's meaning. |
| `list_cert_suites` | — | RO | Lists the certification suites (executable checklists) available: name, FIX version, title, sections and case counts by mode (auto: the agent verifies; assisted: needs the counterparty to act; manual: needs a human). |
| `list_cert_targets` | — | RO | Lists the certification targets (counterparty adapters): name, whether it has a control API (then assisted cases run automatically), and how many cases it marks not applicable. |
| `start_cert_run` | suite, target, session_id, cases?, section?, confirm_external? | SE, open-world | Starts a certification run in the background and returns its run_id at once; the run can take minutes (a whole suite several). |
| `cert_run_status` | run_id | RO | Progress of a certification run: state (running, finished, error), cases done / total, the case running now, and the counts by status so far (PASS, FAIL, BLOCKED, PENDING, N/A, ERROR). |
| `cert_run_results` | run_id, only? | RO | Results of a finished certification run: counts by status (all cases and required cases), the exit code and what it means, the failing and pending cases with their reasons, which cases a human may attest, the drafted deviations section, and where the evidence is on disk (results.json, summary.txt, deviations.md, one folder per case). |
| `attest_cert_case` | run_id, case_id, status, by, note, user_confirmed? | SE, destructive | Records a human's attestation for one manual or assisted case of a finished run (e.g. environment, credentials, sign-off, the deviations review), then recomputes the run's summary and exit code. *(Its next sentences: "ALWAYS ask the human first … only then call this with user_confirmed: true. Never attest on your own judgement, never invent the name …")* |
| `emulator_fill_order` | order_id, qty, price? | SE, open-world | Acts on the counterparty EMULATOR (the OrderEcho test venue), not on a real venue: makes the emulator fill part of one of its working orders at a price you choose, which sends us an execution report. |
| `emulator_cancel_order` | order_id | SE, destructive, open-world | Acts on the counterparty EMULATOR, not on a real venue: the emulator cancels one of its working orders on its own initiative (an unsolicited cancel), which sends us a Canceled execution report. |
| `emulator_hold_order` | order_id | SE, open-world | Acts on the counterparty EMULATOR, not on a real venue: stops the emulator's scheduled reports for one order and leaves it working, so you can then fill, cancel or replace it step by step. |
| `emulator_inject_next` | session, msg_type?, set?, remove? | SE, destructive, open-world | Acts on the counterparty EMULATOR, not on a real venue: makes the emulator alter the next message it sends us on one session (optionally only the next of a MsgType), setting or removing tags, to test how the agent and its checks react to a bad counterparty message. |

Enums sit in the schemas:

| Argument | Values |
|---|---|
| `side` | buy, sell, short |
| `ord_type` | mkt, lmt |
| `tif` | day, gtc, opg, ioc, fok, gtx |
| `wait_for` | none, ack, terminal |
| `direction` | in, out, both |
| `only` | failed, pending, all |
| `status` (`attest_cert_case`) | pass, fail, na |
| `status` (`list_orders`) | open, terminal, all, and each order state |

`qty` takes an integer or a string. `price` takes a number or a string, and its text is kept exactly.

The server also sends MCP `instructions`:
- start with `list_sessions`;
- report verdicts as given;
- cert runs are asynchronous;
- ask the human before attesting or trading an external session;
- the `emulator_*` tools never act on a real venue.

Each result carries two things:
- `structuredContent`: the result object;
- one text block: the plain summary shown in §5.

Each error carries three things:
- `isError: true`;
- the text `ERROR <code>: <detail>` followed by `Hint: <what to do next>`;
- `structuredContent` `{error, detail, hint}`: the same shape as the HTTP API's error body.

## 4. `orderecho mcp install-claude-desktop` (print mode), exactly

```
$ bin/orderecho mcp install-claude-desktop
Add this to /Users/dgavin/Library/Application Support/Claude/claude_desktop_config.json
(merge into "mcpServers" if the file already has other servers), then restart Claude Desktop:

{
  "mcpServers": {
    "orderecho": {
      "args": [
        "mcp",
        "--config",
        "/Users/dgavin/Dropbox/code/GitHub/OrderEcho/config/orderecho.yaml"
      ],
      "command": "/Users/dgavin/Dropbox/code/GitHub/OrderEcho/bin/orderecho",
      "env": {
        "ORDERECHO_HOME": "/Users/dgavin/Dropbox/code/GitHub/OrderEcho"
      }
    }
  }
}

Or run: orderecho mcp install-claude-desktop --write
```
Notes:
- The paths are whatever the shell's working directory reports; here that is the `~/Dropbox` path rather than `~/Library/CloudStorage/Dropbox`. Both reach the same folder.
- `ORDERECHO_HOME` is how the "working directory = repo" requirement is met (see §6.13).
- I did **not** run `--write` against your real Claude Desktop config; the folder doesn't exist on this machine yet. The unit test `TestInstallClaudeDesktop` covers `--write` on a temp file:
  - it creates a new file;
  - it merges into a file that already has another server and a top-level setting, keeping both;
  - it makes a timestamped backup that is byte-identical to the original;
  - a second run changes nothing;
  - it refuses an invalid file and leaves it untouched.

## 5. Real run: service + emulator + an MCP client doing the §8.7 sequence

**Setup:**
- **Emulator:** my own copy (`orderecho_Main.py` from `../OrderEchoFixEmulator`, run as is), with the `orderecho_multi.yaml` rules, static pricing (AAPL 227.50), FIX on 19878/19879 and the control API on 18090.
- **Agent:** the shipped `config/orderecho.yaml` with only the ports changed (sessions → 19878/19879, `service.port` 18190, `emulator.control_api` → 18090). HeartBtInt is 30 s, as shipped.
- **Client:** a throwaway Go program in my scratch directory using `mcp.NewClient` + `mcp.CommandTransport`. It spawns `bin/orderecho mcp --config …/orderecho.yaml`, exactly as Claude Desktop would.
- **Auto-start:** the first `orderecho mcp` (used to list the tools for §3) found no service and started one:
  ```
  orderecho mcp: no agent service on 127.0.0.1:18190; started one (pid 15230, output logs/engine/service_20260930.out)
  orderecho mcp 0.4.0: stdio MCP server, 20 tool(s), service http://127.0.0.1:18190 (pid 15230)
  ```

The §8.7 conversation, over a second stdio session: every tool call and the summary it returned, verbatim.
```
orderecho mcp 0.4.0: stdio MCP server, 20 tool(s), service http://127.0.0.1:18190 (pid 15230)
connected to orderecho 0.4.0 (a4) over stdio
tools/list: 20 tools

-> list_sessions {}
<- 3 session(s): emu42 (FIX.4.2 AGENT -> ORDERECHO) DISCONNECTED [emulator]; emu44 (FIX.4.4 AGENT -> ORDERECHO) DISCONNECTED [emulator]; strict (FIX.4.2 AGENT -> STRICTBRK) DISCONNECTED [emulator]

-> connect_session {"session_id":"emu44"}
<- emu44 logged on to ORDERECHO as AGENT (FIX.4.4, HeartBtInt=30s, next_out=2 next_in=2)

-> send_order {"ord_type":"mkt","qty":100,"session_id":"emu44","side":"buy","symbol":"AAPL","wait_for":"terminal"}
<- sent: BUY 100 AAPL MKT (ClOrdID OE-20260930-105837.141-1, OrderID O-20260930-104005-34) is FILLED, cum 100 avg 227.5000; checks: PASS (11/11 PASS)

-> order_timeline {"cl_ord_id":"last","session_id":"emu44"}
<- order OE-20260930-105837.141-1: D -> 8(New) -> 8(Trade); 11/11 checks PASS, verdict PASS

-> list_cert_suites {}
<- 2 suite(s): order-entry-fix42 (FIX.4.2, 68 cases); order-entry-fix44 (FIX.4.4, 68 cases)

-> list_cert_targets {}
<- 2 target(s): emulator (orderecho-emulator, control API, 11 N/A); generic (generic, no control API, 0 N/A)

-> start_cert_run {"section":"ORD","session_id":"emu44","suite":"order-entry-fix44","target":"emulator"}
<- cert run 20260930-105837.899 started: order-entry-fix44 vs orderecho-emulator on emu44, 9 case(s) (emu44 was connected: the run logged it out and logs on again with a sequence reset); poll cert_run_status

(cert_run_status polled 2 times, every 2s)
-> cert_run_status {"run_id":"20260930-105837.899"}
<- cert run 20260930-105837.899 finished: 9/9 case(s) done (PASS 5, N/A 4); exit code 0 (every required case PASS or N/A)

-> cert_run_results {"only":"all","run_id":"20260930-105837.899"}
<- cert run 20260930-105837.899 (order-entry-fix44 vs orderecho-emulator on emu44): PASS 5, N/A 4; required: PASS 5, N/A 2; exit 0 (every required case PASS or N/A); listed (all): 4.1 PASS, 4.2 PASS, 4.3 PASS, 4.4 PASS, 4.5 N/A, 4.6 N/A, 4.7 PASS, 4.8 N/A, 4.9 N/A

-> session_status {"session_id":"emu44"}
<- emu44 DISCONNECTED next_out=4 next_in=5, 0 open order(s) (last disconnect: Logged out cleanly (initiated by us) (their 58: "Logout acknowledged"))
```
("8(Trade)" is FIX 4.4's 150=F; on 4.2 the same fill reads "8(Fill)".)

The same calls in the service's engine log (§7), one line each, cut at 230 characters here:
```
... INFO    session  engine  MCP  list_sessions {} -> 3 session(s): emu42 (FIX.4.2 AGENT -> ORDERECHO) DISCONNECTED [emulator]; ...
... INFO    session  engine  MCP  connect_session session_id=emu44 -> emu44 logged on to ORDERECHO as AGENT (FIX.4.4, HeartBtInt=30s, next_out=2 next_in=2)
... INFO    session  engine  MCP  send_order ord_type=mkt qty=100 session_id=emu44 side=buy symbol=AAPL wait_for=terminal -> sent: BUY 100 AAPL MKT (ClOrdID OE-20260930-105837.141-1, OrderID O-20260930-104005-34)...
... INFO    session  engine  MCP  order_timeline cl_ord_id=last session_id=emu44 -> order OE-20260930-105837.141-1: D -> 8(New) -> 8(Trade); 11/11 checks PASS, verdict PASS
... INFO    session  engine  MCP  start_cert_run section=ORD session_id=emu44 suite=order-entry-fix44 target=emulator -> cert run 20260930-105837.899 started: ...
... INFO    session  engine  MCP  cert_run_status run_id=20260930-105837.899 -> cert run 20260930-105837.899 running: 0/9 case(s) done
... INFO    session  engine  MCP  cert_run_status run_id=20260930-105837.899 -> cert run 20260930-105837.899 finished: 9/9 case(s) done (PASS 5, N/A 4); exit code 0 ...
```
The same calls also went into the evidence file `data/evidence/service-20260930-105828.677.jsonl` as `tool call: client=mcp-stdio …` events.

The run's folder is `data/certs/20260930-105837.899/`:
- `results.json`, `summary.txt`, `run.json`;
- one folder per executed case: `4.1 4.2 4.3 4.4 4.7`.

`serve --status` while it was up:
```
agent service: running on 127.0.0.1:18190, pid 15230, version 0.4.0 (a4), since 2026-09-30T10:58:28Z
config       : …/scratchpad/smoke/agent/orderecho.yaml
mcp over http: off

SESSION  VERSION  ROUTE               ADDRESS          STATE         ORDERS  OPEN  NOTE
emu42    FIX.4.2  AGENT -> ORDERECHO  127.0.0.1:19878  DISCONNECTED  0       0     emulator:agent42
emu44    FIX.4.4  AGENT -> ORDERECHO  127.0.0.1:19878  DISCONNECTED  1       0     emulator:agent44
strict   FIX.4.2  AGENT -> STRICTBRK  127.0.0.1:19879  DISCONNECTED  0       0     emulator:strict-broker

active cert runs: 0
```
**Stopped afterwards:**
- The service got a SIGTERM and logged `Shutdown: 0 active cert run(s), logging out every session` / `Shutdown complete`.
- My emulator copy got a SIGTERM.
- Nothing listens on 18190/18090/19878/19879 now, and no `orderecho serve` or `orderecho mcp` process is left.
- Your emulator (pid 4282) is still running, untouched.

Earlier smoke tests ran on the same private setup. They covered the HTTP API directly with curl:
- the ZWZZT fill/replace/cancel flow;
- a full-suite run: exit 7 with PASS 46, PENDING 11, N/A 11. 9.1 read "7 of 53 other required case(s) are not PASS or N/A yet: 1.1 PENDING, …". 9.2 drafted 8 N/A reasons and 1 warning into `deviations.md`;
- the three attestation outcomes (unconfirmed, auto case, confirmed);
- a SIGTERM with emu44 connected: a clean 35=5 both ways.

## 6. Decisions (conservative, logged)

1. **SDK and schemas.**
   - `github.com/modelcontextprotocol/go-sdk v1.8.0`, the latest stable release, pinned in go.mod.
   - Its schema library `github.com/google/jsonschema-go v0.4.3` is a direct dependency. It generates the tool schemas from Go structs, and the service validates every call with it.
   - MCP stdio, streamable HTTP and bearer auth (`auth.RequireBearerToken`) are the SDK's own.
2. **One catalogue, one Dispatch.**
   - `internal/service` holds the 20 tools, their schemas, descriptions and handlers.
   - The HTTP API is `POST /api/v1/<tool>`, with a JSON body equal to the MCP arguments.
     - Success: `{"result": …, "summary": …}`.
     - Error: `{error, detail, hint}`, with a 4xx/5xx status.
   - Also served: `GET /api/v1/health`, `/status` (used by `serve --status`) and `/tools`.
   - `orderecho mcp` (stdio) forwards each call to that API. MCP over HTTP calls Dispatch in process.
   - So validation, handlers and result shapes are shared by construction.
3. **Sessions.**
   - Each `connect_session` builds a **fresh agent**, which re-reads seqnums and the message store from disk. A cert run may have changed them in between.
   - Earlier connections are closed but kept, so `list_orders`, `order_timeline` and `"last"` still see their orders (newest connection first).
   - `cancel_order` / `replace_order` act only on orders of the current connection. An older one gets `order_on_old_connection`, with a hint.
   - A per-session lock serializes connect, disconnect and a cert run claiming the session.
4. **A cert run takes the session over.**
   - If the session is connected, the run logs it out. It then runs on its own fresh agent: run id = run_id, sequence reset at start, 60 s case timeout, all as the CLI does.
   - It logs out at the end and leaves the session disconnected.
   - While it runs, order tools and connect/disconnect on that session answer `session_busy`, with a hint to poll.
   - `cert run` (CLI) and `start_cert_run` now share `cert.Execute`, which is why interop 4 can require identical counts.
   - The tool offers what §5 lists (suite, target, session_id, cases, section) plus `confirm_external`. It has no vars, attest file or stop-on-fail.
5. **9.1 (§3.1): PASS / FAIL / PENDING.**
   - PASS iff every *other* required case of the suite is PASS or N/A. The reason lists the ones that aren't, e.g. `2 of 3 other required case(s) are not PASS or N/A yet: m1 PENDING, 9.2 PENDING`.
   - When one isn't: **FAIL** if any of them is FAIL/ERROR, otherwise **PENDING**. This keeps A3's exit codes: exit 7 before attestations, exit 5 on the strict broker.
   - Required cases *not selected* in a partial run count as "not run in this run", so a partial run can never PASS 9.1.
   - 9.1 is decided after every other case, and again after every attestation.
   - An attestation file entry for 9.1 is ignored, and a warning says so.
   - Because of this rule, A3's `checkEmulatorRun` now treats 9.1/9.2 like the manual cases (PENDING without attestations, PASS with them). That is the A4 rule, not a weaker check. I added assertions on 9.1's reason, 9.2's draft and `deviations.md`.
6. **9.2 (§3.2).**
   - Every N/A reason and every warning is grouped by its exact text, with the step prefix removed. Each group lists its cases, in suite order.
   - Warnings about attestations (runner notes, not venue behaviour) are left out.
   - The draft goes into `results.json` (`deviations`) and `deviations.md`.
   - Without an attestation 9.2 is **PENDING**, not BLOCKED; nothing on the counterparty's side is missing.
7. **Suite format:** a new step type, `review: required_cases | deviations`. It must be a case's only step, and the case must be auto (required_cases) or assisted (deviations). The task texts are unchanged, and the drift test passes.
8. **Attestation through the tool.** Accepted only on cases marked `attestable`: manual, assisted without an executable control step, and the deviations review. Refused on:
   - auto cases;
   - assisted cases whose control steps ran;
   - target-N/A cases;
   - NOT_RUN cases;
   - unfinished runs.

   `note` is required. A later attestation replaces an earlier one, and the step list keeps one attestation step. Each attestation:
   - is recorded with `at` and `file: "attest_cert_case (<client>)"`;
   - appends a line to `attestations.jsonl` in the run folder (an audit trail);
   - rewrites `results.json`, `summary.txt` and `deviations.md` and recomputes the exit code;
   - is recorded as an evidence event.
9. **results.json additions:** `attestable`, `review`, `required_in_suite`, `deviations`, `run_error`, `never_logged_on`, `session_dead` and `logout_timeout`. The flags were `json:"-"` before; they are persisted now so a run can be re-scored from disk. `ExitCode` counts `run_error` as a runner ERROR (8).
10. **Shutdown and restart.**
    - **SIGTERM / Ctrl+C.** New mutating calls are refused (`service_stopping`), then:
      - each in-flight run is aborted by logging its session out (`58=OrderEcho agent service stopping`). The case in progress ends, and later cases become NOT_RUN with the reason;
      - the run is marked `error: service stopped while the run was in progress`, and its results are written (exit 8);
      - every connected session is logged out cleanly and the process exits.
    - A second signal exits at once (130).
    - **On start:** every `run.json` still reading `running` is marked `error: service restarted`, along with any partial results.
    - Each service-started run keeps its state in `data/certs/<run_id>/run.json`. CLI runs (results.json only) are readable through the tools too.
11. **Safety.**
    - Beyond the loopback bind, the API refuses non-loopback `Host` headers (DNS rebinding) and any non-loopback `Origin`, and POSTs must be `application/json`. Together these stop a web page from driving the agent.
    - `service.host` exists but must be loopback; otherwise it is a config error.
    - The MCP token comes from `ORDERECHO_MCP_TOKEN`, which wins over `mcp.http_token`. It is compared in constant time.
    - `confirm_external` is also required for `start_cert_run` on an external session, because a run sends real orders.
    - "External" means any host that is not `localhost` or a loopback IP.
12. **Config gates.**
    - `mcp.allow_orders: false` unregisters `send_order`, `cancel_order`, `replace_order` **and `start_cert_run`**, which sends orders.
    - `mcp.allow_emulator_tools: false`, or no `emulator.control_api`, unregisters the four `emulator_*` tools.
    - The gates apply to MCP clients. The service also refuses a disabled tool called over MCP anyway (`tool_disabled`), but the plain `/api/v1` API is not gated.
    - Emulator mapping: sessions have no "target" in the agent config, so a top-level block `emulator: {control_api, sessions: {agent id: emulator id}}` names the sessions whose counterparty is the emulator. `emulator_inject_next` takes our id or the emulator's.
13. **Claude Desktop.**
    - Claude Desktop's server entries have no working-directory field, so the entry sets `env.ORDERECHO_HOME = <repo>`. `orderecho` changes to that directory first; that is how relative `data/`, `logs/` and `certs/` resolve.
    - `command` is `<repo>/bin/orderecho`; if that binary doesn't exist, it falls back to the running executable.
    - `--write`:
      - backs up to `<file>.bak-YYYYMMDD-HHMMSS`;
      - keeps other servers' and settings' JSON values exactly as they were, re-indented;
      - writes top-level keys in sorted order;
      - validates the result before writing, with mode 0600;
      - prints added / replaced (with the old value) / unchanged, and the other servers it kept.
14. **Auto-start.**
    - The service is started detached (its own session), and its stdout/stderr are appended to `logs/engine/service_YYYYMMDD.out`. The engine log itself stays `logs/engine/orderecho_*.log`.
    - `orderecho mcp` waits up to 20 s for `/api/v1/health`.
    - If the service disappears during a conversation, the next call starts a new one and retries once. Interop 6 relies on this.
    - A service of a different version only draws a warning on stderr.
15. **Logging.** Tool calls from MCP clients are logged as `MCP  <tool> <k=v …> -> <summary>`, and plain API calls as `API  …` (errors at WARNING). Every call is also an evidence event in `data/evidence/service-<id>.jsonl`, with the call's session_id as the record's session.
16. **Tool behaviour details.**
    - `send_order` waits for `ack` by default (10 s; `terminal` 30 s; at most 300 s). A timeout is `timed_out: true` with the current state, not an error.
    - `recent_messages` keeps the last 2000 messages per session in memory, across connections. It leaves out 9/10 (framing) from the decoded fields and accepts MsgTypes by code or name.
    - The timeline's checks are the ungraced offline checks, as the REPL's `timeline` (A3 decision 16). `send_order` reports the live graced verdict.
17. **Interop binary and emulator.**
    - The MCP interop tests spawn the binary built by `TestMain` from the same source. That avoids a stale `bin/orderecho` when someone runs `go test -tags interop` without `make`.
    - The real run used a private emulator copy; see the note at the top.

## 7. Questions for you

1. **9.1 when others are only pending:** I made it PENDING (keeping exit 7), and FAIL only when another required case failed. Do you want FAIL whenever it isn't PASS?
2. **`mcp.allow_orders: false` also removes `start_cert_run`.** Is that the right reading of "order tools", or should cert runs stay available?
3. **Should the plain `/api/v1` API also honour `mcp.allow_*`?** Right now only MCP clients are gated; the A5 GUI will use the plain API.
4. **Orders from an earlier connection** can be listed and timelined, but not canceled or replaced (the new agent has no state for them). Is that acceptable, or should reconnect carry the order book over?
5. **Claude Desktop working directory** is set through `ORDERECHO_HOME` in `env`. Is that fine, or would you rather `--config` imply the repo (its parent's parent)?
6. **`start_cert_run` extras:** do you want `vars`, an attestation file and `stop_on_fail` exposed through the tool as well? The CLI has them.
7. **Your emulator on the standard ports:** as in A3, it was running. I kept my traffic off it by using my own copy on other ports. Is that the behaviour you want from now on?
