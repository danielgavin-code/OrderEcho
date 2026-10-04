# OrderEcho (Go agent) — Cook A5 Build Spec
**HTML certification report + local web GUI on the agent service**

## 0. Ground rules
- No git commands. Build only §2 scope. Never modify `../OrderEchoFixEmulator`.
- Bump to `0.5.0` / `a5`. No new MCP tools except `cert_run_report` (§3.4); existing tools unchanged.
- No external CDNs, fonts or network calls from any page; assets embedded with `go:embed`.
- Report per §9 into REPORT_A5.md.

## 1. Context
The agent certifies sessions and an LLM can drive it via MCP. A5 adds the two things a human needs: a **certification report** you could hand to a counterparty or compliance, and a **GUI** to watch and operate the agent. Both read from the A4 service — no second source of truth. The report is generated from `data/certs/<run_id>/` (results.json + per-case evidence) and must be **evidence-backed and tamper-evident**.
Carry-over: A4 question 1 — keep 9.1 as PENDING when other required cases are only pending.

## 2. Scope
In: §3 report, §4 GUI, §5 live updates, §6 security, §7 shared style, §8 tests.
Out: in-GUI LLM chat (A6), ChatGPT connector, multi-user/auth beyond §6.

## 3. Certification report
3.1 `orderecho cert report <run_id|results_dir> [--open]` writes `data/certs/<run_id>/report.html`; also generated automatically when a run finishes. Single self-contained file (inline CSS, no JS required to read it; small optional JS for expand/collapse only). Deterministic: same results → byte-identical HTML.
3.2 Contents, in order:
- **Header**: suite title, target, session (version, CompIDs, host:port), run id, start/end (UTC), agent version/build, emulator/counterparty identification if known.
- **Verdict banner**: CERTIFIED / NOT CERTIFIED / INCOMPLETE (pending/blocked required cases) with the counts by status (all and required).
- **Summary table by section** (counts per status).
- **Case table**: id, title, required, mode, status badge, reason; each case expandable to: steps with timestamps and outcome, expectations matched (which message satisfied them), timeline checks (11 with explanations), attestation (who/when/note), and its **FIX evidence** — decoded one-line view plus raw messages (monospace, wrapped), limited to that case's window.
- **Deviations** (from 9.2 draft) and **N/A list** with reasons.
- **Attestations** section listing every human attestation.
- **Integrity appendix**: SHA-256 of `results.json` and every per-case `fix.log`/`evidence.jsonl`; `orderecho cert verify <run_id>` recomputes and reports any mismatch (exit 0 ok / 9 tampered). Hashes are written into `results.json` at run end.
- Print stylesheet: clean A4/Letter output via the browser's Print → PDF (page breaks between sections, expanded evidence when printing).
3.3 Service: `GET /api/v1/certs/{run_id}/report` serves it; `GET /api/v1/certs` lists runs.
3.4 MCP tool `cert_run_report {run_id}` → path, URL (`http://127.0.0.1:8190/certs/<run_id>/report`), verdict, counts.

## 4. GUI (served by `orderecho serve` at `http://127.0.0.1:8190/`)
Vanilla HTML/CSS/JS, embedded. Pages (left nav):
- **Dashboard**: sessions (state, version, route, seqnums, heartbeat age, open orders), connect/disconnect buttons; recent cert runs with verdict badges; service version.
- **Orders**: per-session list (state, cum/leaves/avg, live check status); **send order** form (symbol, side, qty, type, price, TIF); cancel / replace actions; click → timeline modal with the 11 checks.
- **Messages**: live decoded feed per session (direction, seq, type, key fields, badges for PossDup/injected/bad framing), filters (session, type, ClOrdID, rejects only), click for full decode.
- **Certifications**: suites/targets; start-run form (suite, target, session, section/cases); live progress (current case, counts); results table; **attest** manual/pending cases (status, name, note — explicit confirm dialog); open report; verify integrity button.
- **Emulator** panel (only when a control API is configured): fill / hold / cancel an order, inject next, seq gap — clearly labelled "acts on the counterparty emulator".
- **About**: versions, config path, data/log paths, MCP setup hint (the Claude Desktop snippet command).
All actions call the same `/api/v1` endpoints as MCP (no GUI-only logic).

## 5. Live updates
- Server-Sent Events: `GET /api/v1/events` streams session state changes, inbound/outbound message summaries, order updates (with check status), cert run progress. Reconnects automatically; bounded per-client buffer (drop oldest, tell the client).
- Pages update live without reload; manual refresh still works.

## 6. Security (loopback service, but browsers can be tricked)
- Bind loopback only (unchanged).
- Every state-changing request (POST/PUT/DELETE) requires a per-process **CSRF token** (served to the page, sent in a header) **and** an `Origin`/`Host` check allowing only the service's own origin. GET endpoints have no side effects.
- MCP-over-HTTP bearer token unchanged; GUI and MCP-HTTP are independent.
- Attestation via GUI records `by` from the form and the client as `gui`.

## 7. Shared style
- `web/orderecho.css` with a `:root` token block (same token names/approach as the emulator's Cook 8 stylesheet so a future design pass can restyle both) — copy the emulator's token names, not its files at runtime.
- Status badge colors consistent everywhere: PASS, FAIL, WARN, PENDING, BLOCKED, N/A, ERROR, CERTIFIED/NOT CERTIFIED/INCOMPLETE.
- Monospace for FIX; readable at 1280px and down to ~1000px.

## 8. Tests
- Report: golden test from a fixed results fixture (byte-identical HTML); contains every case id; verdict logic (CERTIFIED/NOT/INCOMPLETE); no external URLs; print CSS present; `cert verify` passes, then detects a one-byte edit in a case `fix.log` (exit 9).
- GUI/API: every page served; assets embedded; CSRF missing/invalid → 403; foreign Origin → 403; GETs side-effect free; SSE stream delivers events in order and survives a reconnect.
- Interop (real emulator): start the service, connect emu42 via the API, send an order via the API (as the GUI form does), receive SSE events for its reports; run the ORD section via the API → report generated automatically → `cert_run_report` via MCP returns the URL; attest a manual case via the API with CSRF → report regenerates with the attestation.
- `make test`, `make lint`, `make interop` pass; interop must actually run.

## 9. Report → REPORT_A5.md
1. Files. 2. Test outputs with counts. 3. Real run: service + emulator; a full Order Entry run (emulator target, emu42) with an attestation file; generate the report; paste the report's header/verdict/summary as text, its size, and the `cert verify` output; list the GUI pages with what each shows. Stop everything afterwards. 4. Design notes (tokens + components) for a later visual pass. 5. Decisions. 6. Questions.
