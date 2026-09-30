# OrderEcho (Go agent) — Cook A3 Build Spec
**Certification runner: YAML suites from FIXReader's cert scripts, auto/assisted/manual cases, per-case evidence, A2 follow-ups**

## 0. Ground rules
- No git commands. Build only §2 scope. Never modify `../OrderEchoFixEmulator` or `certs/source/*.html`.
- Bump to `0.3.0` / `a3`. Interop tests use the emulator with **static pricing**, temp storage, random ports.
- Report per §10 into REPORT_A3.md.

## 1. Context
`certs/source/cert_order_entry.html` is FIXReader's Order Entry certification checklist (FIX 4.2, US equities). Its `SECTIONS` and `ROWS` JS arrays hold every step (`section, step, area, task, level, req`). A3 turns that checklist into an executable suite and a runner that produces **evidence-backed** per-step results. Drop Copy and Allocation (also in `certs/source/`) are **out of scope** (later cooks) — but the format must not preclude them.
Pass/fail comes from code: explicit expectations + the 11 order checks. Never from an LLM.

## 2. Scope
In: §3 A2 follow-ups, §4 suite format, §5 the Order Entry suite, §6 runner, §7 evidence/results, §8 CLI, §9 tests.
Out: Drop Copy/Allocation suites, API/MCP (A4), HTML report/GUI (A5).

## 3. A2 follow-ups
3.1 **Closed-range ResendRequest**: on a gap, request `7=expected`, `16=<received seq − 1>`; hold the triggering (and later) messages; process them in order once the range is filled. (Replaces A2's "process held messages covered by a gap fill".)
3.2 **Duplicate-request rejects form their own chain**: a report whose OrderID differs from a chain's established OrderID and is a reject (39=8) of a ClOrdID reused from that chain is split into its own chain, so the original order's verdict is unaffected. Parity: Python still has the old behaviour — mark those parity cases as a documented, expected divergence (to be fixed on the Python side later).
3.3 **Live grace**: in live mode, `requests_answered` does not WARN for requests younger than `answer_grace_sec` (config, default 5). Offline analysis unchanged.
3.4 **Run ID with milliseconds** (`YYYYMMDD-HHMMSS.mmm` style, filesystem-safe) so ClOrdIDs never collide across runs.

## 4. Suite format (`certs/*.yaml`)
```yaml
suite: order-entry-fix42
title: Order Entry Certification — FIX 4.2 US Equities
source: certs/source/cert_order_entry.html
fix_version: FIX.4.2
vars: { symbol: AAPL, qty: 100, limit_buy: 1.00, limit_sell: 99999.00 }   # overridable per target / CLI
sections: [ { id: MSG, name: Message Flow }, ... ]
cases:
  - id: "4.1"
    section: ORD
    title: "New Order Single — Market Buy"
    task: "<verbatim task text from the checklist>"
    required: true
    level: basic
    mode: auto            # auto | assisted | manual
    steps:
      - send: { msg: D, side: buy, ord_type: mkt, symbol: "{{symbol}}", qty: "{{qty}}" }
      - expect: { msg: "8", exec_type: NEW, within: 5s }
      - expect: { msg: "8", ord_status: [PARTIALLY_FILLED, FILLED], within: 10s, optional: true }
      - assert_sent: { absent: [44] }        # our D had no Price tag
      - checks: timeline                    # the 11 checks must not FAIL
```
Step types (keep the set small and composable):
- `send` — D/F/G via the normal path (`ref: <name>` to name an order; `cancel`/`replace` take `order: <name>`), or `raw: [[tag,val],…]` via SendRaw for deliberately bad messages.
- `expect` — wait for an inbound message matching fields; semantic names for ExecType/OrdStatus/Side are mapped per FIX version (e.g. `exec_type: TRADE` matches `150=1/2` in 4.2 and `150=F` in 4.4); literal tags allowed (`tags: {103: "3"}`); `within`; `optional`.
- `session` — `testreq`, `logout`, `reconnect`, `disconnect_abrupt`, `skip_outbound_seq: n`, `resend_request: {begin, end}`, `wait: 35s`.
- `control` — counterparty-side action. Against a target with a control API (emulator), call it (e.g. `{post: "/sessions/{session}/test-request"}`); against any other target the case becomes `assisted` and the runner records `BLOCKED: needs counterparty action: <description>` unless an attestation is supplied.
- `assert_sent` / `assert_received` — tag presence/absence/values on a named message.
- `checks: timeline` — run the 11 checks on the case's order chains (FAIL → case FAIL; WARN → case PASS with warning).
- `manual` — `prompt: "<what a human must confirm>"`; result comes from attestation.

## 5. The Order Entry suite — `certs/order_entry_fix42.yaml`
- Hand-author (you, reading the HTML once) one case per checklist ROW, preserving `step` as `id`, the task text verbatim, section, `req`, `level`.
- Mode assignment: environment/credentials/sign-off rows → `manual`; rows needing the counterparty to initiate or confirm something → `assisted` with `control` steps where the emulator can do it; everything else the agent can drive and verify → `auto`.
- Map the **Message Flow, Order Entry, Order Lifecycle, Cancel/Replace, Reject Scenarios, Recovery** rows to concrete steps. For reject rows, use `raw` sends to provoke the reject (missing tag, bad value, duplicate ClOrdID, cancel unknown order) and expect the exact FIX response (35=3 with 373/371, 35=8 39=8 with 103, 35=9 with 102).
- Include a `4.2`-specific `vars` block; a `certs/order_entry_fix44.yaml` variant may reuse cases (YAML anchors or an `extends:` field) with `fix_version: FIX.4.4`.
- **Drift test**: parse the HTML's `ROWS` array; every `step` must exist in the suite (and vice versa), with matching task text.

**Targets** — `certs/targets/<name>.yaml` adapt a suite to a counterparty:
```yaml
target: orderecho-emulator
control_api: http://127.0.0.1:8090          # present => control steps executable
vars: { symbol: AAPL, hold_symbol: ZWZZT, reject_symbol: KLM, partial_symbol: EFG }
not_applicable:
  "4.5": "Emulator supports TimeInForce Day only"
  "4.6": "Emulator supports TimeInForce Day only"
  "4.8": "Emulator supports TimeInForce Day only"
  "4.9": "Emulator supports TimeInForce Day only"
```
Ship `certs/targets/emulator.yaml` (above, completed from what the emulator really supports) and `certs/targets/generic.yaml` (no control API, no N/A overrides).

## 6. Runner — `internal/cert`
- Runs selected cases in suite order on one session (reconnecting when a case requires it), each case isolated: fresh `ref` names, its own ClOrdIDs, a case start/end marker in evidence.
- Per case result: `PASS`, `FAIL` (with the failing step + reason), `BLOCKED` (needs counterparty/human and no attestation), `N/A` (target override, with reason), `PENDING` (manual, no attestation yet), `ERROR` (runner/infra problem, distinct from FAIL).
- Attestations: `--attest <file.yaml>` mapping `step: {status: pass|fail|na, by: <name>, note: <text>}`; applied to manual/assisted cases only; recorded verbatim in results (who/when/note).
- A case never waits forever: every `expect` has a timeout; overall `--case-timeout` default 60s.
- Continue after a failing case by default (`--stop-on-fail` optional).

## 7. Evidence and results
`data/certs/<run_id>/`:
- `results.json` — suite, target, session, version, run_id, start/end, counts by status, per-case `{id, title, required, mode, status, reason, steps:[{type, status, detail, ts}], orders:[ClOrdIDs], checks, attestation}`.
- `<case_id>/fix.log` — the FIX log lines for that case's time window; `<case_id>/evidence.jsonl` — its evidence records.
- `summary.txt` — human table (id, title, req, mode, status, reason).
This directory is the input A5's HTML report will render.

## 8. CLI
- `orderecho cert list` → suites and targets found.
- `orderecho cert show --suite <file>` → cases with mode/required.
- `orderecho cert run --suite <file> --target <file> --session <id> [--case 4.1,4.3 | --section ORD] [--attest file] [--stop-on-fail] [--var k=v]` → live progress one line per case, then the summary table and the results dir path. Exit: `0` all required cases PASS or N/A; `5` any required FAIL; `7` no required FAIL but some required BLOCKED/PENDING; `8` runner ERROR; session exit codes (1/3/4) when the session itself fails.

## 9. Tests
**Unit**: suite parsing/validation (unknown step type, bad field → clear error with case id); variable substitution; semantic ExecType/OrdStatus mapping 4.2 vs 4.4; expect matching and timeouts (FakeClock); attestation application; result aggregation and exit codes; drift test against the HTML; §3 follow-ups (closed-range resend, duplicate-reject chain split, grace, run id).
**Interop (real emulator)**:
1. Full Order Entry suite vs the emulator target on `agent42`: every `auto` case PASS or N/A; `assisted` cases with emulator control steps PASS; `manual` cases PENDING; exit 7 (because manual steps are pending). Then again with an attestation file covering all manual steps → exit 0.
2. Same suite on `agent44` with the FIX 4.4 variant → same outcome.
3. **Negative control**: run against `strict-broker` (rejects all orders) → order cases FAIL with clear reasons, session cases PASS, exit 5.
4. Generic target (no control API) → assisted cases BLOCKED with their descriptions.
5. A deliberately broken case in a test-only suite (expects FILLED on a hold symbol) → FAIL with the step and timeout reason; ERROR path covered separately (e.g. emulator killed mid-case).
`make test`, `make lint`, `make interop` all pass; report counts; interop must actually run.

## 10. Report → REPORT_A3.md
1. Files created/changed. 2. Test outputs with counts.
3. Real run against the emulator (`config/orderecho_multi.yaml`, live pricing is fine): `cert run` on agent42 with the emulator target, then with an attestation file; the strict-broker negative control; paste the summary tables and one case's evidence folder listing + its `fix.log`. Stop everything afterwards.
4. Mapping table: every checklist row → mode + one-line description of how it's verified (or why manual/N/A).
5. Decisions I made. 6. Questions for me.
