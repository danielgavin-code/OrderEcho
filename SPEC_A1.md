# OrderEcho (Go agent) — Cook A1 Build Spec
**FIX initiator: codec, session layer, 4.2/4.4 profiles, persistence, evidence & logs, CLI — interop-tested against the Python emulator**

## 0. Ground rules
- No git commands. Build only §2 scope. Never modify `../OrderEchoFixEmulator`.
- Go 1.27 (installed). Standard library preferred; allowed deps: `gopkg.in/yaml.v3`. **No FIX library** — we own the session layer so later cooks can send deliberately bad messages.
- Report per §10 into REPORT_A1.md.

## 1. Context
OrderEcho is the agent an LLM operates to run FIX certifications. It is the **initiator / buy side**. It must work against any FIX counterparty; the Python emulator in `../OrderEchoFixEmulator` is the test partner (FIX on 9878/9879, control API on 8090).
Carry over the emulator's proven design: a **pure, deterministic session core** (inputs + injected clock → actions; no sockets, no `time.Now()`), a thin transport, and **identical evidence/log formats** so the emulator's Python log viewer reads the agent's logs.

## 2. Scope
In: §3 layout, §4 codec, §5 session core, §6 profiles, §7 persistence/evidence/logs, §8 config + CLI, §9 tests.
Out: sending orders (A2), cert runner (A3), API/MCP (A4), GUI/report (A5). Application messages received (e.g. ERs) are logged/evidenced and handed to an app hook that is a no-op in A1.

## 3. Layout
```
cmd/orderecho/            main: subcommands (§8)
internal/version/         Version = "0.1.0", Build = "a1"
internal/clock/           SystemClock, FakeClock
internal/fix/codec/       framing, 9/10 validation, encode, pipe display
internal/fix/profile/     FIX42, FIX44 profiles
internal/fix/session/     PURE session core
internal/fix/transport/   TCP initiator: connect, read loop, timers, reconnect
internal/store/           seqnum store, outbound message store
internal/evidence/        JSONL evidence writer
internal/logs/            FIX log + engine log writers
internal/config/          YAML load + validation
testdata/                 fixtures
Makefile                  build / test / interop / lint (go vet)
```
Binary: `bin/orderecho` via `make build`.

## 4. Codec (`internal/fix/codec`)
- Incremental decode from a byte stream; own framing (BeginString → BodyLength → CheckSum); verify 9 and 10; bad frames returned as `DiscardedFrame{Raw, Reason}` (never dropped silently).
- Messages keep **ordered fields with repeats**.
- Encode: header order `8, 9, 35, 49, 56, 34, 52, [43, 122]`, body, `10`. SendingTime `YYYYMMDD-HH:MM:SS.sss` UTC.
- `ToPipe(raw)`.
- **Cross-language fixture**: `testdata/emulator_lines.txt` contains real lines copied from the emulator's FIX logs (take ~20 from `../OrderEchoFixEmulator/logs/fix/`: Logon, Heartbeat, D, 8 in both 4.2 and 4.4, Logout; convert `|` back to SOH in the test). Go must decode each and re-encode **byte-identical**.

## 5. Session core (`internal/fix/session`, PURE)
Interface mirrors the emulator: `OnConnect() / OnMessage(msg) / OnDiscarded(frame) / OnTimer() / OnDisconnect() / InitiateLogout(text)` → `[]Action` (`Send`, `Disconnect`, `Evidence`). Session owns sequence numbers and persists via the store.
Initiator behavior:
- **Connect** → send Logon: `98=0`, `108=<heartbeat_sec>`, `141=Y` if `reset_on_logon`. State `LOGON_SENT`.
- **Logon reply** within `logon_timeout_sec` (default 10) else Disconnect. Validate BeginString, CompIDs (mirrored), `108`. Counterparty Logout instead of Logon → record its `58` in evidence (e.g. "Incorrect BeginString…") and disconnect; this must surface clearly in CLI output.
- **Sequence rules** exactly as the emulator's Cook 1 §7.4 (gap → one ResendRequest `7=expected 16=0`; lower without PossDup → Logout+Disconnect; lower with PossDup → ignore), including on the Logon reply (gap on Logon → accept, then ResendRequest).
- Heartbeat / TestRequest (`TEST-<n>` counter) / TestRequest timeout, same as emulator Cook 1 §7.7.
- Inbound SequenceReset gap-fill and reset modes, as emulator Cook 1 §7.6.
- **Inbound ResendRequest → replay** from the outbound message store, emulator Cook 4 §6 rules: admin types (`0 1 2 3 4 5 A`) and missing seqs collapse into gap fills; application messages resent with `43=Y`, `122`=original 52, new 52, same 34; bounded `16`.
- Inbound application messages: evidence + FIX log + `app.OnAppMessage(msg)` (no-op in A1). Never BusinessMessageReject them.
- Inbound session Reject (35=3): evidence + engine log WARNING.
- Logout: we initiate (`LOGOUT_SENT`, timeout → disconnect) or they initiate (reply, then disconnect).
- **Test-only injection hooks** (not exposed on CLI yet): `SkipOutboundSeq(n)` (advance next_out without sending — creates a gap the counterparty will ask about) and `SendRaw(fields)` (arbitrary message through the normal send path, evidence `injected: true`).

## 6. Profiles (`internal/fix/profile`)
`FIX42`, `FIX44`: BeginString, admin/application MsgType sets, enum names needed for logging. Session-layer tag content is identical for our scope; the profile is the single place version differences will live (A2 adds order-message rendering).

## 7. Persistence, evidence, logs
- Seqnums: `data/seqnums/<session_id>.json` (atomic writes). Outbound store: `data/msgstore/<session_id>.jsonl` (raw with SOH, JSON-escaped), archived with a timestamp suffix on sequence reset (141=Y or `--reset`).
- **Evidence JSONL** — same schema as the emulator (`ts, run_id, kind in|out|discarded|event, session, seq, msg_type, raw (| delimited), fields [[tag,value],…], detail, injected, order`), one file per run: `data/evidence/<run_id>.jsonl`.
- **FIX log** — the emulator's exact line format (Cook 1 §10A: timestamp, `IN`/`OUT`/`DISC`, seq, MsgType, raw last, `# injected:` suffix), file `logs/fix/<session_id>_<YYYYMMDD>.log`, daily UTC rollover, flush per line. IN/OUT are from the **agent's** point of view.
- Engine log: `logs/engine/orderecho_<YYYYMMDD>.log`, same line shape as the emulator's.
- Console echo of FIX lines and INFO+ engine lines (config switch).

## 8. Config + CLI
`config/orderecho.yaml` (shipped), mirroring the emulator's multi-session config:
```yaml
sessions:
  - { id: emu42,  fix_version: FIX.4.2, sender_comp_id: AGENT, target_comp_id: ORDERECHO, host: 127.0.0.1, port: 9878, heartbeat_sec: 30, reset_on_logon: true }
  - { id: emu44,  fix_version: FIX.4.4, sender_comp_id: AGENT, target_comp_id: ORDERECHO, host: 127.0.0.1, port: 9878, heartbeat_sec: 30, reset_on_logon: true }
  - { id: strict, fix_version: FIX.4.2, sender_comp_id: AGENT, target_comp_id: STRICTBRK, host: 127.0.0.1, port: 9879, heartbeat_sec: 30, reset_on_logon: true }
defaults: { logon_timeout_sec: 10, logout_timeout_sec: 10, heartbeat_grace_pct: 20, reconnect: false, reconnect_interval_sec: 5 }
storage: { seqnum_dir: data/seqnums, msgstore_dir: data/msgstore, evidence_dir: data/evidence }
logging: { log_dir: logs, fix_delimiter: "|", engine_level: INFO, console: true }
```
Validation errors name the session and key. Per-session overrides of `defaults` keys allowed.
CLI (`orderecho <cmd>`; global `--config`):
- `version` → version + build.
- `sessions` → table of configured sessions.
- `connect --session <id> [--duration 30s] [--test-request] [--reset] [--reconnect]` → log on, stay connected (Ctrl+C → clean Logout; second Ctrl+C exits), optional one TestRequest after logon, exit code 0 on clean logout, 1 on refused/failed logon (print the counterparty's Logout text), 2 on config error.
- `status --session <id>` → persisted seqnums and store size (offline).

## 9. Tests
**Unit (pure core, FakeClock, no network)** — mirror the emulator's Cook 1 §12 session tests adapted to the initiator: logon sent on connect with/without 141; logon reply validation (CompIDs, BeginString, 108); logon timeout; counterparty Logout instead of Logon; gap on logon reply; heartbeat/TestRequest/timeout; gap → single ResendRequest; seq too low ± PossDup; SequenceReset both modes; ResendRequest replay (admin runs collapsed, app messages replayed, bounded 16); both logout directions; SkipOutboundSeq and SendRaw evidence. Codec round-trip, framing errors, cross-language fixture.
**Interop (real Python emulator)** — a Go test helper starts `../OrderEchoFixEmulator/.venv/bin/python ../OrderEchoFixEmulator/orderecho_Main.py --config <generated tmp config>` with **all storage in a temp dir** and **random free ports** (FIX + control API), waits for `/health`, and kills it at the end. If the emulator folder or venv is missing, `t.Skip` with a clear message (but they exist here — the report must show interop tests ran, not skipped). Scenarios:
1. Logon to a 4.2 session and a 4.4 session; TestRequest round trip; clean logout both directions (emulator `POST /sessions/{id}/logout`).
2. Emulator-initiated TestRequest (`POST /sessions/{id}/test-request`) answered.
3. Emulator `POST /sessions/{id}/inject/seq-gap {"skip":3}` → agent sends ResendRequest → emulator gap-fills → both sides in sync (next TestRequest round trip succeeds).
4. Agent `SkipOutboundSeq(3)` → emulator sends ResendRequest → agent replays/gap-fills correctly → in sync.
5. Emulator `POST /sessions/{id}/disconnect` → with `reconnect: true` the agent reconnects and logs on **without** reset; seqnums continue correctly.
6. Wrong version: agent 4.4 against the strict (4.2) session → Logout "Incorrect BeginString…" surfaced, CLI exit 1.
7. Unknown CompIDs → connection dropped, CLI exit 1 with a clear message.
8. **Viewer interop**: run the emulator's `orderecho_LogView.py stats <agent FIX log>` and `view --no-color` → every agent message parsed, 0 unparseable, directions correct; the evidence JSONL also parses.
`make test` runs unit tests; `make interop` runs interop tests (build tag `interop`). Both must pass. Include `go vet` clean.

## 10. Report → REPORT_A1.md
1. Files created. 2. `make test` and `make interop` full output (show interop tests actually ran). 3. Real run: start the emulator with its `config/orderecho_multi.yaml`; `bin/orderecho connect --session emu42 --test-request --duration 10s` and the same for `emu44`; then `--session strict` with a 4.4 override to show the BeginString refusal; paste console output and the Python viewer's `view` of the agent's FIX log. Stop everything afterwards. 4. Decisions I made. 5. Questions for me.
