# OrderEcho (Go agent) — Cook A2 Build Spec
**Orders (D/F/G), shadow order state, the 11 timeline checks ported to Go with Python parity, A1 follow-ups**

## 0. Ground rules
- No git commands. Build only §2 scope. Never modify `../OrderEchoFixEmulator` (reading/importing its Python modules in tests is fine).
- Bump version to `0.2.0` / build `a2`.
- Emulator interop tests use **static pricing** in their generated configs (deterministic prices).
- Report per §9 into REPORT_A2.md.

## 1. Context
A1 gave us a correct FIX initiator. A2 makes the agent trade: send NewOrderSingle / Cancel / Cancel-Replace, track every order in a **shadow state** built from what we sent and what the counterparty reported, and judge the counterparty's reports with the **same 11 checks** the emulator's Python viewer uses (`../OrderEchoFixEmulator/orderecho_Timeline.py`). Go and Python must agree exactly — that parity is our proof the certification logic is right.

## 2. Scope
In: §3 A1 follow-ups, §4 order messages, §5 shadow state + live checks, §6 checks port + parity, §7 CLI, §8 tests.
Out: cert runner (A3), API/MCP (A4), GUI/report (A5).

## 3. A1 follow-ups
3.1 **Out-of-order queue.** Messages arriving with a seq *above* expected are held (bounded: 1000 messages per session; overflow → Logout "Resend queue overflow" + disconnect) while a ResendRequest is outstanding, then processed in seq order as soon as the gap is closed (by replayed messages or gap fill). A TestRequest that arrives behind a gap is therefore answered after the fill. Evidence records queue/dequeue.
3.2 **HeartBtInt mismatch**: config `heartbeat_mismatch: warn | refuse` (default `warn`: engine log WARNING, keep our own interval).
3.3 **Exit codes** (all commands that connect): `0` clean, `1` logon refused/failed, `2` config/usage error, `3` dropped after logon, `4` logout timeout, `5` order checks FAIL, `6` timed out waiting for an order to reach a terminal state. Document in `--help`.
3.4 `--fix-version FIX.4.2|FIX.4.4` override on every connecting command.
3.5 When the counterparty answers our Logon with a Logout, **reply with Logout**, then disconnect (still exit 1 with their text).

## 4. Order messages (rendered by the version profile)
- **ClOrdIDs**: `<clordid_prefix>-<run_id>-<n>` (config `clordid_prefix`, default `OE`), per-run counter, unique across sessions.
- **D**: `11, 21=1 (always in 4.2; in 4.4 only if config include_handl_inst: true — default true), 55, 54, 60, 38, 40, 44 if limit, 59 if given, 1 if configured`.
- **F**: `41, 11, 37 if known, 55, 54, 60, 38`.
- **G**: `41, 11, 37 if known, 21 (as D), 55, 54, 60, 38, 40, 44 if limit`.
- Side/OrdType/TIF inputs are validated client-side for **normal** sends; `SendRaw` (A1) remains the way to send deliberately bad messages.
- Golden fixtures for D/F/G in both versions (`testdata/golden/`), captured once and held byte-identical.

## 5. Shadow state and live checks — `internal/order`
- Pure order manager: tracks every order we send, keyed by its ClOrdID chain (D → G… → F), with OrderID learned from the first report. States: `SENT`, `NEW`, `PARTIALLY_FILLED`, `FILLED`, `CANCELED`, `REJECTED`, `PENDING_CANCEL`, `PENDING_REPLACE`, `REPLACED` history.
- Applies inbound 35=8 and 35=9; session rejects (35=3) and business rejects (35=j) referencing our request seq are attached to that request.
- Our **own** expectation: `CumQty_expected = Σ LastQty`, `AvgPx_expected = Σ(LastQty×LastPx)/Σ LastQty` (Decimal-exact — use `math/big` or a fixed-point decimal; no float64 for money), `Leaves_expected = OrderQty − Cum` while working.
- After **each** inbound report, run the §6 checks on the chain so far; any FAIL/WARN is logged immediately (engine log + evidence `event` with the check name and explanation) — not just at the end.
- Reports for ClOrdIDs we don't know → evidence WARNING (`unsolicited or unknown order`), still logged and still checkable offline.
- Evidence: after each 35=8/35=9, an `event` record whose `order` field holds the shadow snapshot + latest check statuses.

## 6. The 11 checks — `internal/checks` (PURE)
- Port `orderecho_Timeline.py` faithfully: the chain builder (41 links both directions, shared 37, 35=3 by RefSeqNum) and all 11 checks (`cum_qty_monotonic`, `working_quantities`, `terminal_quantities`, `fill_quantities_sum`, `avg_px`, `exec_ids_unique`, `order_id_constant`, `nothing_after_terminal`, `version_rules`, `requests_answered`, `framing_intact`), same names, same PASS/WARN/FAIL semantics, same explanations wherever practical, verdict = worst.
- ERs are identified by MsgType (35=8 is always the sell side's report), not by log direction — so checks work on both the emulator's logs and the agent's logs. If the Python implementation depends on direction in a way that changes a verdict on agent logs, **do not change the Go behaviour to match a Python bug**: log it as a finding in the report.
- Works on live messages (from §5) and on parsed log files (a Go parser for the OrderEcho FIX log format and evidence JSONL; generic raw FIX lines too).
- **Parity harness** (test only): runs the emulator's Python checks by invoking its `.venv` Python with a small inline script that imports `orderecho_LogParse` / `orderecho_Timeline` from `../OrderEchoFixEmulator` and prints JSON `{verdict, checks:[{name,status}]}` for a given file + order. Go must produce the same verdict and the same per-check statuses.

## 7. CLI
- `orderecho order --session <id> <SYM> <QTY> <buy|sell|short> <mkt|lmt> [PX] [--tif day] [--wait 15s]` → connect, send, print each report as it arrives with the live check line, wait for a terminal state (or `--wait`), print the timeline + verdict, logout. Exit 0 PASS/WARN, 5 FAIL, 6 timeout.
- `orderecho session --session <id>` → interactive (and pipe-friendly) REPL: `order …`, `cancel <ClOrdID|last>`, `replace <ClOrdID|last> <QTY> [PX]`, `status`, `timeline <ClOrdID|last>`, `resend <b> [e]`, `testreq`, `quit`; `last` = most recently sent order. Ctrl+C = clean logout. Prints inbound messages as they arrive.
- `orderecho timeline <files…> (--clordid X | --order-id X) [--json]` → offline checks on any log; exit 0 PASS, 1 WARN, 2 FAIL (same as the Python viewer).

## 8. Tests
**Unit**: D/F/G golden (4.2 + 4.4); order manager transitions; decimal math; every check with passing and failing hand-crafted chains (port the Python test cases); out-of-order queue (hold, release after fill, overflow); heartbeat mismatch; exit-code mapping; Logout reply to refused Logon.
**Interop (real emulator, static pricing, temp storage, random ports)** — each scenario ends with a Go verdict:
1. Full fill (A–D rule) on 4.2 and 4.4 → PASS; 4.4 fills are `150=F`.
2. Partials (E–G) → PASS, order left working.
3. Odd lots with `fill_rest` (N–P: 1,2,3,405,remainder) → PASS, AvgPx exact.
4. Hold (ZWZZT) → `replace last 800 10.50` → `cancel last` → PASS; chain has D, G, F.
5. Rule reject (K–M) and band reject (limit far from static ref) → PASS with REJECTED states; strict session rejects everything.
6. Unsolicited cancel (H–J rule) → PASS.
7. Manual fills via emulator control API (`POST /orders/{id}/fill` twice at different prices) → AvgPx exact → PASS.
8. Emulator `inject/next` adds `9999=FOO` to an ER → agent tolerates the unknown tag, verdict unaffected.
9. Duplicate ClOrdID via `SendRaw` → `103=6` reject received and attached.
10. **TestRequest behind a gap**: emulator `inject/seq-gap` then `test-request` → agent answers with the matching `112` after the gap fill (§3.1).
11. Cancel of an unknown ClOrdID via `SendRaw` → 35=9 `102=1` attached to the request.
**Parity**: for every interop scenario's agent log and emulator log, Go and Python agree (verdict + per-check). Plus tampered variants (one mutation per check where applicable, e.g. CumQty edit, duplicated ExecID, OrderID change, extra fill after terminal) → both FAIL/WARN identically.
`make test`, `make lint`, `make interop` all pass; interop/parity must actually run (report counts).

## 9. Report → REPORT_A2.md
1. Files created/changed. 2. `make test`, `make lint`, `make interop` output with counts (parity cases listed).
3. Real run with the emulator on `config/orderecho_multi.yaml`: `orderecho order --session emu44 AAPL 100 buy mkt`; then a piped `orderecho session --session emu42` doing: `order EFG 1000 buy lmt 10.00`, `order ZWZZT 500 buy lmt 10.00`, `replace last 800 10.50`, `cancel last`, `status`, `timeline last`, `quit`. Paste outputs and one Python-vs-Go timeline comparison. Stop everything afterwards.
4. Decisions I made. 5. Questions for me. 6. Any Python-side findings (bugs in the emulator's checks) — reported, not fixed.
