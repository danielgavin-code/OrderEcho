# OrderEcho (Go agent) — Cook A4 Build Spec
**Agent service + MCP server (stdio for Claude Desktop, HTTP for later ChatGPT) — the LLM becomes the operator**

## 0. Ground rules
- No git commands. Build only §2 scope. Never modify `../OrderEchoFixEmulator`.
- Bump to `0.4.0` / `a4`.
- New dependency allowed: the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`), pinned. If it can't support what §5 needs, stop and report rather than hand-rolling a protocol.
- Report per §9 into REPORT_A4.md.

## 1. Context
The agent now trades and certifies from the CLI. A4 lets an LLM operate it through MCP tools. FIX sessions must stay up between tool calls and cert runs outlast a tool call, so a long-running **agent service** owns all state; MCP is a thin, safe front door. The same service API will back the A5 GUI.
Principle unchanged: the LLM decides *what to do*; the agent's code decides *what happened* and *whether it passed*.

## 2. Scope
In: §3 small carry-overs, §4 agent service, §5 MCP server + tools, §6 Claude Desktop setup helper, §7 safety, §8 tests.
Out: GUI + HTML report (A5), in-GUI chat (A6), ChatGPT connector auth specifics (finalized later against current ChatGPT docs — build the HTTP transport + bearer token now).

## 3. Carry-overs from A3
3.1 Case **9.1** becomes `auto`: PASS iff every required case is PASS or N/A (with the list of any that aren't).
3.2 Case **9.2** becomes `assisted`: the runner drafts a **deviations** section (all warnings + N/A reasons, grouped) into `results.json` and `deviations.md`; still needs attestation to PASS.

## 4. Agent service — `orderecho serve`
- Binds **loopback only** (default `127.0.0.1:8190`, config `service.port`); refuses non-loopback.
- Owns: all configured sessions (connect/disconnect on demand, multiple at once), their order managers, cert runs (async, persisted under `data/certs/<run_id>/` as in A3), attestations.
- JSON HTTP API under `/api/v1/…` mirroring the §5 tools one-to-one (same handlers, same validation, same result shapes). Errors: `{error, detail, hint}` where `hint` tells the caller what to do next.
- Runs survive client disconnects; a cert run continues if the MCP client goes away. Service restart: sessions start disconnected; finished cert runs remain readable from disk; runs that were in progress are marked `ERROR: service restarted`.
- Ctrl+C / SIGTERM: log out all sessions cleanly, mark in-flight runs ERROR, exit.
- `orderecho serve --status` → is it running, sessions, active runs.

## 5. MCP server — `orderecho mcp`
- **stdio** transport (for Claude Desktop): `orderecho mcp` connects to the running service; if none is running, it starts one in the background (detached, logs to `logs/engine/`) and waits for its health check.
- **HTTP** transport: `orderecho serve --mcp-http` also serves MCP at `/mcp` on the service port, **requiring** `Authorization: Bearer <token>` (config `mcp.http_token` or env `ORDERECHO_MCP_TOKEN`; refuse to enable without one).
- Tools (names, JSON schemas, and descriptions written **for an LLM reader**: what it does, when to use it, what comes back, common mistakes). Every result has structured content **and** a short plain-text summary.
  - `list_sessions` — configured sessions with version, route, state.
  - `connect_session {session_id, reset?}` / `disconnect_session {session_id}`.
  - `session_status {session_id}` — state, seqnums, heartbeat, open orders, last messages summary.
  - `send_order {session_id, symbol, side, qty, ord_type, price?, tif?, wait_for?: none|ack|terminal, timeout_sec?}` — returns ClOrdID, reports received so far, shadow state, live check status.
  - `cancel_order {session_id, cl_ord_id|"last"}` / `replace_order {session_id, cl_ord_id|"last", qty?, price?}`.
  - `list_orders {session_id, status?}`.
  - `order_timeline {session_id, cl_ord_id|order_id}` — chain + the 11 checks with explanations.
  - `recent_messages {session_id, limit?, direction?, msg_types?}` — decoded (names, not just tags).
  - `list_cert_suites` / `list_cert_targets`.
  - `start_cert_run {suite, target, session_id, cases?, section?}` → `run_id` immediately.
  - `cert_run_status {run_id}` — progress (done/total, current case, counts so far).
  - `cert_run_results {run_id, only?: failed|pending|all}` — summary counts, failing/pending cases with reasons, deviations draft, results path.
  - `attest_cert_case {run_id, case_id, status: pass|fail|na, by, note, user_confirmed: true}` — records a human attestation and recomputes the summary. The description must tell the LLM to **ask the human first**; calls without `user_confirmed: true` are rejected with a hint.
  - **Emulator-only tools** (registered only when a session's target has a control API configured, e.g. `emulator.control_api` in config): `emulator_fill_order {order_id, qty, price?}`, `emulator_cancel_order {order_id}`, `emulator_hold_order {order_id}`, `emulator_inject_next {session, set?, remove?, msg_type?}` — each clearly described as acting on the *counterparty emulator*, not on a real venue.
- MCP tool annotations: read-only tools marked read-only; order/cancel/replace/emulator/attest tools marked as having side effects.

## 6. Claude Desktop setup helper
- `orderecho mcp install-claude-desktop` prints the JSON snippet for `~/Library/Application Support/Claude/claude_desktop_config.json` (absolute path to `bin/orderecho`, args `["mcp", "--config", "<absolute config path>"]`, working directory = repo). With `--write`: back up the existing file (timestamped), merge the `orderecho` server entry without touching other servers, validate JSON, print what changed.

## 7. Safety
- Service and HTTP MCP bind loopback only.
- Config `mcp.allow_orders` (default true) and `mcp.allow_emulator_tools` (default true); when false those tools are not registered.
- Sessions pointing at a non-loopback host are tagged `external` in `list_sessions`; order tools on an external session require an explicit `confirm_external: true` argument (description tells the LLM to ask the human first).
- Every tool call is logged in the engine log (`MCP  <tool> <args summary> -> <result summary>`) and as an evidence event.

## 8. Tests
**Unit**: tool schemas validate; argument validation and hints; `last` resolution; annotations; external-session confirm; attestation without `user_confirmed` rejected; token required on HTTP; 9.1 auto and 9.2 draft.
**Interop (real emulator + real MCP client from the Go SDK)**:
1. stdio: spawn `bin/orderecho mcp` (which auto-starts the service), `list_sessions`, `connect_session emu42`, `send_order AAPL 100 buy mkt wait_for=terminal` → FILLED + checks PASS, `order_timeline last`, `recent_messages`.
2. Order flow: `send_order ZWZZT … wait_for=ack` → `emulator_fill_order` twice at different prices → `order_timeline` AvgPx exact → `cancel_order last` on the remainder.
3. Replace flow: hold symbol → `replace_order last qty=800 price=10.50` → timeline chain D→G.
4. Cert via tools: `start_cert_run order-entry-fix42 emulator emu42 section=ORD` → poll `cert_run_status` to completion → `cert_run_results` counts match a CLI run of the same section; `attest_cert_case` on a manual case (with and without `user_confirmed`).
5. HTTP MCP: without token → 401; with token → same tools work.
6. Service restart mid-run → run marked ERROR; finished runs still readable.
7. **Scripted operator conversation**: a test that performs, via MCP only, the sequence an LLM would for "connect emu44, buy 100 AAPL at market, then certify the order-entry section" and asserts every step's result.
`make test`, `make lint`, `make interop` pass; interop must actually run.

## 9. Report → REPORT_A4.md
1. Files. 2. Test outputs with counts. 3. Tool list with one-line descriptions (as the LLM sees them). 4. Output of `orderecho mcp install-claude-desktop` (print mode) — the exact snippet. 5. Real run: service + emulator, then an MCP client session (the Go SDK client or the MCP inspector if available) performing the §8.7 sequence; paste the tool calls and summaries. Stop everything afterwards. 6. Decisions. 7. Questions.
