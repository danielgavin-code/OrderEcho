# REPORT_A3 — OrderEcho Go agent, Cook A3

A3 is built end to end:
- the certification runner (`internal/cert`);
- the Order Entry suite: all 68 checklist rows, hand-authored, with the task text verbatim and held to the HTML by a drift test;
- a FIX 4.4 variant (via `extends:`);
- emulator and generic targets;
- `orderecho cert list|show|run`;
- evidence-backed results in `data/certs/<run_id>/`;
- the four A2 follow-ups.

Version is `0.3.0` / build `a3`.

`make test`, `make lint` and `make interop` all pass. **Interop: 29/29 tests PASS, 0 skipped, against the real emulator.** Parity: **81/81 cases agree**, plus **7 documented divergences**, all behaving exactly as documented.

**Please read §3.0.** As in A2, the emulator on the standard ports during the real run was yours, so I used it and did not stop it.

## 1. Files created / changed

New:
```
certs/order_entry_fix42.yaml     the Order Entry suite: 68 cases, one per checklist row
certs/order_entry_fix44.yaml     FIX 4.4 variant (extends the 4.2 suite)
certs/targets/emulator.yaml      the emulator: control API, session map, symbols, 11 N/A overrides
certs/targets/generic.yaml       any counterparty: no control API, no N/A overrides
internal/cert/suite.go           suite format: loader (scalars kept verbatim, anchors/merge keys, extends), validation naming the case
internal/cert/semantic.go        ExecType/OrdStatus/Side names per FIX version, {vars}, targets, attestations
internal/cert/driver.go          wire history, the Driver interface, the live driver over internal/agent
internal/cert/runner.go          case execution: send/expect/session/control/assert/checks/manual, statuses, exit codes
internal/cert/results.go         results.json, summary.txt, per-case fix.log/evidence.jsonl slices
internal/cert/cert_test.go       unit tests (fake driver on a FakeClock), drift test against the HTML
cmd/orderecho/cert.go            orderecho cert list | show | run
internal/interop/cert_test.go    A3 interop scenarios 1-5
REPORT_A3.md
```
Changed:
```
internal/version/version.go            0.3.0 / a3
internal/fix/session/session.go(+test) 3.1 closed-range ResendRequest; held Logon seq consumed after the fill; follow-up
                                       request for a further gap; ResendStored (deliberate PossDup resend)
internal/checks/chain.go, checks.go    3.2 duplicate-reject chain split; 3.3 RequestsAnsweredWithGrace / WithAnswerGrace
internal/checks/checks_test.go         split tests
internal/order/order.go(+test)         3.3 live grace; GTX (59=5); per-order Account; CancelOpt(allowTerminal)
internal/evidence/evidence.go(+test)   3.4 run id YYYYMMDD-HHMMSS.mmm
internal/config/config.go              answer_grace_sec (default 5); storage.certs_dir (default data/certs)
internal/agent/agent.go                OnWire hook, grace, CancelAny
internal/fix/transport/transport.go    OnWire (every framed message in/out), Rearm (reconnect after Logout/Stop)
config/orderecho.yaml                  answer_grace_sec, certs_dir
cmd/orderecho/main.go                  cert command, exit codes in --help
internal/interop/interop_test.go       A1 scenario 3 now expects the closed range (3.1)
internal/interop/orders_test.go        A2 scenario 9: Go PASS, a documented Python divergence (3.2)
internal/interop/parity_harness_test.go, parity_test.go   documented divergences counted and listed
```
Untouched: `certs/source/*.html` and `../OrderEchoFixEmulator`, apart from your running emulator's own runtime files (§3.0).

## 2. Test outputs

### `make test`

Unit tests by package: agent 1, cert 16 (including the drift test), checks 32, config 4, evidence 2, codec 12, session 52, logs 5, order 15, store 4. That is **143 PASS, 0 FAIL, 0 SKIP**.
```
go test -count=1 ./...
?   	github.com/danielgavin-code/OrderEcho/cmd/orderecho	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/agent	0.846s
ok  	github.com/danielgavin-code/OrderEcho/internal/cert	1.516s
ok  	github.com/danielgavin-code/OrderEcho/internal/checks	1.772s
?   	github.com/danielgavin-code/OrderEcho/internal/clock	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/config	2.370s
ok  	github.com/danielgavin-code/OrderEcho/internal/evidence	1.128s
ok  	github.com/danielgavin-code/OrderEcho/internal/fix/codec	1.994s
?   	github.com/danielgavin-code/OrderEcho/internal/fix/profile	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/fix/session	1.685s
?   	github.com/danielgavin-code/OrderEcho/internal/fix/transport	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/logs	1.776s
ok  	github.com/danielgavin-code/OrderEcho/internal/order	1.675s
ok  	github.com/danielgavin-code/OrderEcho/internal/store	1.963s
?   	github.com/danielgavin-code/OrderEcho/internal/version	[no test files]
```
The unit tests cover:
- suite validation errors (unknown step type, bad fields, unknown names/sections, mode/step mismatches, undefined order refs), each naming the case;
- verbatim scalars (`1.00` stays `1.00`) and variable layering (suite < target < `--var`);
- semantic ExecType/OrdStatus for 4.2 vs 4.4;
- expect matching, `any_of`, `none`, `optional`, the order filter and timeouts, on a FakeClock;
- fail-fast on a reject;
- the case timeout;
- a session drop becoming ERROR, and exit 3 when the session cannot come back;
- manual/assisted/N/A handling with attestations, exit codes 0/1/3/4/5/7/8, and `--stop-on-fail`;
- results writing;
- the drift test against the HTML;
- the §3 follow-ups: closed range, logon-gap seq consumption, a further gap, `ResendStored`, the chain split, the grace, run IDs within the same second.

### `make lint`
```
go vet ./...
go vet -tags interop ./...
```

### `make interop`

Result: **29 tests, 29 PASS, 0 SKIP** (212 s).
```
--- PASS: TestCertEmulatorFIX42 (68.96s)
--- PASS: TestCertEmulatorFIX44 (39.37s)
--- PASS: TestCertStrictBrokerNegativeControl (32.39s)
--- PASS: TestCertGenericTargetBlocksAssisted (13.12s)
--- PASS: TestCertBrokenCaseAndError (6.33s)
--- PASS: TestCLIOrderExitCodes (4.65s)
--- PASS: TestCLISessionPipedAndTimeline (1.65s)
--- PASS: TestScenario1LogonTestRequestLogout (3.96s)
--- PASS: TestScenario2EmulatorTestRequest (1.10s)
--- PASS: TestScenario3EmulatorSeqGap (1.15s)
--- PASS: TestScenario4AgentSkipOutboundSeq (1.05s)
--- PASS: TestScenario5ReconnectWithoutReset (1.65s)
--- PASS: TestScenario6WrongVersion (1.45s)
--- PASS: TestScenario7UnknownCompIDs (0.95s)
--- PASS: TestScenario8ViewerReadsAgentLogs (1.76s)
--- PASS: TestCtrlCLogsOutCleanly (1.31s)
--- PASS: TestA2Scenario01FullFill (4.90s)
--- PASS: TestA2Scenario02Partials (3.61s)
--- PASS: TestA2Scenario03OddLots (3.69s)
--- PASS: TestA2Scenario04HoldReplaceCancel (2.98s)
--- PASS: TestA2Scenario05Rejects (2.06s)
--- PASS: TestA2Scenario06UnsolicitedCancel (1.67s)
--- PASS: TestA2Scenario07ManualFills (1.15s)
--- PASS: TestA2Scenario08UnknownTagTolerated (1.65s)
--- PASS: TestA2Scenario09DuplicateClOrdID (1.05s)
--- PASS: TestA2Scenario10TestRequestBehindGap (1.76s)
--- PASS: TestA2Scenario11CancelUnknown (1.06s)
--- PASS: TestParityTampered (3.69s)
--- PASS: TestParityKnownDivergences (0.06s)
PASS
ok  	github.com/danielgavin-code/OrderEcho/internal/interop	212.460s
```
Parity:
```
PARITY SUMMARY: 81 case(s) compared, 81 agree, 0 disagree, 7 documented divergence(s) as expected
  AGREE    CLI session / agent FIX log / OE-20260929-090549.026-1                 go=PASS py=PASS [all PASS]
  AGREE    CLI session / emulator FIX log / OE-20260929-090549.026-1              go=PASS py=PASS [all PASS]
  AGREE    CLI session / agent FIX log / OE-20260929-090549.026-2                 go=PASS py=PASS [all PASS]
  AGREE    CLI session / emulator FIX log / OE-20260929-090549.026-2              go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent FIX log / IT-20260929-090604.829-emu428-1         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent evidence / IT-20260929-090604.829-emu428-1        go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator FIX log / IT-20260929-090604.829-emu428-1      go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator evidence / IT-20260929-090604.829-emu428-1     go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent FIX log / IT-20260929-090604.829-emu428-2         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent evidence / IT-20260929-090604.829-emu428-2        go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator FIX log / IT-20260929-090604.829-emu428-2      go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator evidence / IT-20260929-090604.829-emu428-2     go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent FIX log / IT-20260929-090606.920-emu449-1         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent evidence / IT-20260929-090606.920-emu449-1        go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator FIX log / IT-20260929-090606.920-emu449-1      go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator evidence / IT-20260929-090606.920-emu449-1     go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent FIX log / IT-20260929-090606.920-emu449-2         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent evidence / IT-20260929-090606.920-emu449-2        go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator FIX log / IT-20260929-090606.920-emu449-2      go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator evidence / IT-20260929-090606.920-emu449-2     go=PASS py=PASS [all PASS]
  AGREE    A2-2 / agent FIX log / IT-20260929-090609.934-emu4210-1                go=PASS py=PASS [all PASS]
  AGREE    A2-2 / agent evidence / IT-20260929-090609.934-emu4210-1               go=PASS py=PASS [all PASS]
  AGREE    A2-2 / emulator FIX log / IT-20260929-090609.934-emu4210-1             go=PASS py=PASS [all PASS]
  AGREE    A2-2 / emulator evidence / IT-20260929-090609.934-emu4210-1            go=PASS py=PASS [all PASS]
  AGREE    A2-3 / agent FIX log / IT-20260929-090613.336-emu4411-1                go=PASS py=PASS [all PASS]
  AGREE    A2-3 / agent evidence / IT-20260929-090613.336-emu4411-1               go=PASS py=PASS [all PASS]
  AGREE    A2-3 / emulator FIX log / IT-20260929-090613.336-emu4411-1             go=PASS py=PASS [all PASS]
  AGREE    A2-3 / emulator evidence / IT-20260929-090613.336-emu4411-1            go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / agent FIX log / IT-20260929-090617.028-emu4212-1        go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / agent evidence / IT-20260929-090617.028-emu4212-1       go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / emulator FIX log / IT-20260929-090617.028-emu4212-1     go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / emulator evidence / IT-20260929-090617.028-emu4212-1    go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / agent FIX log / IT-20260929-090617.871-emu4413-1        go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / agent evidence / IT-20260929-090617.871-emu4413-1       go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / emulator FIX log / IT-20260929-090617.871-emu4413-1     go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / emulator evidence / IT-20260929-090617.871-emu4413-1    go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent FIX log / IT-20260929-090620.317-emu4214-1                go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent evidence / IT-20260929-090620.317-emu4214-1               go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator FIX log / IT-20260929-090620.317-emu4214-1             go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator evidence / IT-20260929-090620.317-emu4214-1            go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent FIX log / IT-20260929-090620.317-emu4214-2                go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent evidence / IT-20260929-090620.317-emu4214-2               go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator FIX log / IT-20260929-090620.317-emu4214-2             go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator evidence / IT-20260929-090620.317-emu4214-2            go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / agent FIX log / IT-20260929-090620.741-strict15-1        go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / agent evidence / IT-20260929-090620.741-strict15-1       go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / emulator FIX log / IT-20260929-090620.741-strict15-1     go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / emulator evidence / IT-20260929-090620.741-strict15-1    go=PASS py=PASS [all PASS]
  AGREE    A2-6 / agent FIX log / IT-20260929-090622.071-emu4416-1                go=PASS py=PASS [all PASS]
  AGREE    A2-6 / agent evidence / IT-20260929-090622.071-emu4416-1               go=PASS py=PASS [all PASS]
  AGREE    A2-6 / emulator FIX log / IT-20260929-090622.071-emu4416-1             go=PASS py=PASS [all PASS]
  AGREE    A2-6 / emulator evidence / IT-20260929-090622.071-emu4416-1            go=PASS py=PASS [all PASS]
  AGREE    A2-7 / agent FIX log / IT-20260929-090623.745-emu4217-1                go=PASS py=PASS [all PASS]
  AGREE    A2-7 / agent evidence / IT-20260929-090623.745-emu4217-1               go=PASS py=PASS [all PASS]
  AGREE    A2-7 / emulator FIX log / IT-20260929-090623.745-emu4217-1             go=PASS py=PASS [all PASS]
  AGREE    A2-7 / emulator evidence / IT-20260929-090623.745-emu4217-1            go=PASS py=PASS [all PASS]
  AGREE    A2-8 / agent FIX log / IT-20260929-090624.893-emu4418-1                go=PASS py=PASS [all PASS]
  AGREE    A2-8 / agent evidence / IT-20260929-090624.893-emu4418-1               go=PASS py=PASS [all PASS]
  AGREE    A2-8 / emulator FIX log / IT-20260929-090624.893-emu4418-1             go=PASS py=PASS [all PASS]
  AGREE    A2-8 / emulator evidence / IT-20260929-090624.893-emu4418-1            go=PASS py=PASS [all PASS]
  DIVERGE  A2-9 / agent FIX log / IT-20260929-090626.542-emu4219-1                go=PASS py=FAIL [order_id_constant: go PASS, py FAIL] (documented, expected: A3 3.2 duplicate-reject chain split; Python not yet updated)
  DIVERGE  A2-9 / agent evidence / IT-20260929-090626.542-emu4219-1               go=PASS py=FAIL [order_id_constant: go PASS, py FAIL] (documented, expected: A3 3.2 duplicate-reject chain split; Python not yet updated)
  DIVERGE  A2-9 / emulator FIX log / IT-20260929-090626.542-emu4219-1             go=PASS py=FAIL [order_id_constant: go PASS, py FAIL] (documented, expected: A3 3.2 duplicate-reject chain split; Python not yet updated)
  DIVERGE  A2-9 / emulator evidence / IT-20260929-090626.542-emu4219-1            go=PASS py=FAIL [order_id_constant: go PASS, py FAIL] (documented, expected: A3 3.2 duplicate-reject chain split; Python not yet updated)
  AGREE    A2-10 / agent FIX log / IT-20260929-090627.593-emu4220-1               go=PASS py=PASS [all PASS]
  AGREE    A2-10 / agent evidence / IT-20260929-090627.593-emu4220-1              go=PASS py=PASS [all PASS]
  AGREE    A2-10 / emulator FIX log / IT-20260929-090627.593-emu4220-1            go=PASS py=PASS [all PASS]
  AGREE    A2-10 / emulator evidence / IT-20260929-090627.593-emu4220-1           go=PASS py=PASS [all PASS]
  AGREE    A2-11 / agent FIX log / RAWF-dlroiz11a1bs                              go=PASS py=PASS [all PASS]
  AGREE    A2-11 / agent evidence / RAWF-dlroiz11a1bs                             go=PASS py=PASS [all PASS]
  AGREE    A2-11 / emulator FIX log / RAWF-dlroiz11a1bs                           go=PASS py=PASS [all PASS]
  AGREE    A2-11 / emulator evidence / RAWF-dlroiz11a1bs                          go=PASS py=PASS [all PASS]
  AGREE    tampered agent FIX log: cum_qty_monotonic (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [cum_qty_monotonic=FAIL working_quantities=FAIL]
  AGREE    tampered agent FIX log: working_quantities (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [working_quantities=FAIL]
  AGREE    tampered agent FIX log: terminal_quantities (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [terminal_quantities=FAIL]
  AGREE    tampered agent FIX log: fill_quantities_sum (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [fill_quantities_sum=FAIL]
  AGREE    tampered agent FIX log: avg_px (IT-20260929-090630.412-emu4422-1)      go=FAIL py=FAIL [avg_px=FAIL]
  AGREE    tampered agent FIX log: exec_ids_unique (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [exec_ids_unique=FAIL]
  AGREE    tampered agent FIX log: order_id_constant (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    tampered agent FIX log: nothing_after_terminal (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [fill_quantities_sum=FAIL nothing_after_terminal=FAIL]
  AGREE    tampered agent FIX log: version_rules (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [version_rules=FAIL]
  AGREE    tampered agent FIX log: version_rules (IT-20260929-090630.412-emu4422-2) go=FAIL py=FAIL [version_rules=FAIL]
  AGREE    tampered agent FIX log: requests_answered (IT-20260929-090630.412-emu4422-2) go=WARN py=WARN [requests_answered=WARN]
  AGREE    tampered agent FIX log: framing_intact (IT-20260929-090630.412-emu4422-2) go=WARN py=WARN [framing_intact=WARN]
  AGREE    tampered agent evidence: avg_px (IT-20260929-090630.412-emu4422-1)     go=FAIL py=FAIL [avg_px=FAIL]
  DIVERGE  divergence 1: own Reject counted as an answer                          go=WARN py=PASS [requests_answered: go WARN, py PASS] (documented, expected)
  DIVERGE  divergence 2: tag 110= truncates the message                           go=PASS py=WARN [framing_intact: go PASS, py WARN] (documented, expected)
  DIVERGE  divergence 3: non-ASCII value breaks the checksum                      go=PASS py=WARN [framing_intact: go PASS, py WARN] (documented, expected)

```
Scenario verdicts:
```
SCENARIO VERDICTS (Go, live):
  A3-1 cert FIX 4.2 emulator (no attest)       exit 7  PASS 46, PENDING 11, N/A 11
  A3-1 cert FIX 4.2 emulator (attested)        exit 0  PASS 54, N/A 14
  A3-2 cert FIX 4.4 emulator                   exit 7  PASS 46, PENDING 11, N/A 11
  A3-3 cert strict broker (negative control)   exit 5  PASS 24, FAIL 22, PENDING 11, N/A 11
  A3-4 cert generic target (assisted BLOCKED)  exit 7  PASS 1, BLOCKED 8
  A3-5 broken case (FAIL + reason)             exit 5  step 3 (expect): timed out after 3s waiting for ord_status=FILLED from the counterparty; last relevant message: 35=8 seq=2 11=OE-20260929-090537.940-1 37=O-20260929-090537-1 150=0 39=0 32=0 14=0 151=100 6=0.0000
  A3-5 emulator killed mid-case                exit 3  B.2/B.3 ERROR (session dropped)
  1 full fill FIX.4.2 (AAPL mkt)               IT-20260929-090604.829-emu428-1 FILLED            PASS 
  1 full fill FIX.4.2 (CSCO lmt day)           IT-20260929-090604.829-emu428-2 FILLED            PASS 
  1 full fill FIX.4.4 (AAPL mkt)               IT-20260929-090606.920-emu449-1 FILLED            PASS 
  1 full fill FIX.4.4 (CSCO lmt day)           IT-20260929-090606.920-emu449-2 FILLED            PASS 
  2 partials EFG (left working)                IT-20260929-090609.934-emu4210-1 PARTIALLY_FILLED  PASS 
  3 odd lots NOK 1/2/3/405/589                 IT-20260929-090613.336-emu4411-1 FILLED            PASS 
  4 hold/replace/cancel ZWZZT FIX.4.2          IT-20260929-090617.028-emu4212-1 CANCELED          PASS 
  4 hold/replace/cancel ZWZZT FIX.4.4          IT-20260929-090617.871-emu4413-1 CANCELED          PASS 
  5 rule reject KO                             IT-20260929-090620.317-emu4214-1 REJECTED          PASS 
  5 band reject AAPL lmt 500                   IT-20260929-090620.317-emu4214-2 REJECTED          PASS 
  5 strict broker rejects all                  IT-20260929-090620.741-strict15-1 REJECTED          PASS 
  6 unsolicited cancel HON                     IT-20260929-090622.071-emu4416-1 CANCELED          PASS 
  7 manual fills 300@10 + 200@11               IT-20260929-090623.745-emu4217-1 PARTIALLY_FILLED  PASS 
  8 ER with 9999=FOO                           IT-20260929-090624.893-emu4418-1 FILLED            PASS 
  9 duplicate ClOrdID via SendRaw              IT-20260929-090626.542-emu4219-1 NEW               PASS 
  10 TestRequest behind gap, then AAPL         IT-20260929-090627.593-emu4220-1 FILLED            PASS 
  11 cancel unknown ClOrdID via SendRaw        RAWF-dlroiz11a1bs              (no order)        PASS
  tamper base: NOK odd lots (4.4)              IT-20260929-090630.412-emu4422-1 FILLED            PASS 
  tamper base: ZWZZT replace/cancel (4.4)      IT-20260929-090630.412-emu4422-2 CANCELED          PASS 
```
The cert tests map onto the spec's interop scenarios like this:

| §9 | Test | What it shows |
|---|---|---|
| 1 | `TestCertEmulatorFIX42` | 68 cases on agent42. Every auto case PASS or N/A; every assisted case the emulator can drive PASS, via its control API (each such case's control step returned HTTP 200); manual cases PENDING; exit 7. Case 7.7's folder holds both Ds and the 103=6 reject, bracketed by the case markers. Re-run with an attestation file covering the 11 manual rows: exit 0, attestations recorded. |
| 2 | `TestCertEmulatorFIX44` | The 4.4 variant on agent44 gives the same outcome, exit 7. A 4.2 suite on a 4.4 session is refused, exit 2. |
| 3 | `TestCertStrictBrokerNegativeControl` | On strict-broker every order case FAILs with "the counterparty rejected the order … Strict broker rejects everything"; the session cases PASS; exit 5. |
| 4 | `TestCertGenericTargetBlocksAssisted` | Generic target: the 8 assisted cases are `BLOCKED: needs counterparty action: <description>`; the auto case PASSes; exit 7. |
| 5 | `TestCertBrokenCaseAndError` | A test-only case expecting FILLED on a hold symbol FAILs: "step 3 (expect): timed out after 3s waiting for ord_status=FILLED …; last relevant message: 35=8 … 39=0". Killing the emulator mid-case gives B.2 and B.3 ERROR ("session dropped"), and exit 3 because the session could not be re-established. |

<details><summary>Full <code>make interop</code> output</summary>

```
go build -o bin/orderecho ./cmd/orderecho
go test -count=1 -tags interop -v ./internal/interop/
=== RUN   TestCertEmulatorFIX42
    cert_test.go:123: emulator up: fix=54941 strict=54942 api=54943 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCertEmulatorFIX423322479136/001/emulator
    cert_test.go:125: $ orderecho cert run --suite certs/order_entry_fix42.yaml --target certs/targets/emulator.yaml --session emu42  -> exit 7
        OrderEcho 0.3.0 (a3) cert run 20260929-090316.180
          suite   : order-entry-fix42 (FIX.4.2, 68 cases) certs/order_entry_fix42.yaml
          target  : orderecho-emulator certs/targets/emulator.yaml
          session : emu42 AGENT -> ORDERECHO @ 127.0.0.1:54941
        
        [ 1/68] 1.1   manual   PENDING  Certification environment details and documentation — needs a human attestation: Confirm the cert host, port and the venue's documentation package were received.
        [ 2/68] 1.2   auto     PASS     TCP connectivity to the cert host
        [ 3/68] 1.3   auto     PASS     Outbound firewall permits FIX egress
        [ 4/68] 1.4   manual   PENDING  Inbound rules for a different response IP/port — needs a human attestation: Confirm whether the venue pushes responses from a different IP/port range, and that inbound rules allow it (or N/A).
        [ 5/68] 1.5   manual   PENDING  CompIDs, password and sequence reset policy collected — needs a human attestation: Confirm SenderCompID, TargetCompID, session password and the venue's sequence reset policy were provisioned and recorded.
        [ 6/68] 1.6   manual   PENDING  TLS vs clear-text requirement confirmed — needs a human attestation: Confirm with the venue's onboarding team whether TLS is required (this agent speaks clear-text TCP).
        [ 7/68] 1.7   manual   PENDING  UAT endpoint confirmed — needs a human attestation: Confirm the configured host/port is the venue's UAT (certification) endpoint, not production.
        [ 8/68] 2.1   auto     PASS     BeginString configured
        [ 9/68] 2.2   auto     PASS     SenderCompID / TargetCompID as provisioned
        [10/68] 2.3   auto     PASS     HeartBtInt configured
        [11/68] 2.4   auto     PASS     EncryptMethod = 0
        [12/68] 2.5   auto     PASS     ResetOnLogon for the cert session
        [13/68] 2.6   auto     PASS     Clean sequence state before the cert
        [14/68] 2.7   auto     PASS     ResetSeqNumFlag=Y accepted on reconnect
        [15/68] 3.1   auto     PASS     Logon acknowledged
        [16/68] 3.2   auto     PASS     Bidirectional heartbeats at HeartBtInt
        [17/68] 3.3   auto     PASS     TestRequest answered with matching Heartbeat
        [18/68] 3.4   assisted PASS     Exchange-initiated TestRequest answered
        [19/68] 3.5   auto     PASS     ResendRequest replayed with PossDupFlag=Y
        [20/68] 3.6   auto     PASS     Sequence gap bridged with SequenceReset-GapFill
        [21/68] 3.7   auto     PASS     Graceful Logout acknowledged
        [22/68] 3.8   auto     PASS     Recovery after a disconnect without Logout
        [23/68] 4.1   auto     PASS     New Order Single — Market Buy
        [24/68] 4.2   auto     PASS     New Order Single — Market Sell
        [25/68] 4.3   auto     PASS     New Order Single — Limit Buy
        [26/68] 4.4   auto     PASS     New Order Single — Limit Sell
        [27/68] 4.5   auto     N/A      IOC order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [28/68] 4.6   auto     N/A      FOK order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [29/68] 4.7   auto     PASS     Day order
        [30/68] 4.8   auto     N/A      GTC order persists across session restart — target orderecho-emulator: Emulator supports TimeInForce Day only
        [31/68] 4.9   auto     N/A      GTX order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [32/68] 5.1   auto     N/A      Pending New before the acknowledgement — target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
        [33/68] 5.2   auto     PASS     New acknowledgement
        [34/68] 5.3   assisted PASS     Partial fill
        [35/68] 5.4   auto     PASS     Full fill
        [36/68] 5.5   assisted N/A      Done For Day — target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
        [37/68] 5.6   assisted N/A      Expired — target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
        [38/68] 5.7   auto     PASS     ExecIDs unique across the session
        [39/68] 5.8   assisted PASS     AvgPx and CumQty accumulate across partial fills
        [40/68] 6.1   auto     PASS     Order Cancel Request references ClOrdID and OrigClOrdID
        [41/68] 6.2   auto     PASS     Cancel acknowledgement
        [42/68] 6.3   auto     PASS     Cancel Reject for a fully-filled order
        [43/68] 6.4   assisted N/A      Cancel Reject — too late to cancel (order pending fill) — target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
        [44/68] 6.5   auto     PASS     Cancel/Replace — price only
        [45/68] 6.6   auto     PASS     Cancel/Replace — quantity up
        [46/68] 6.7   auto     PASS     Cancel/Replace — quantity down
        [47/68] 6.8   auto     N/A      Pending Replace then Replace ack — target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
        [48/68] 7.1   auto     PASS     Missing required tag — Session Reject
        [49/68] 7.2   auto     PASS     Invalid MsgType — reject
        [50/68] 7.3   auto     PASS     Invalid Symbol — reject
        [51/68] 7.4   auto     PASS     Invalid Price — reject
        [52/68] 7.5   auto     PASS     Invalid Side — reject with RefTagID=54
        [53/68] 7.6   auto     N/A      Invalid Account — reject — target orderecho-emulator: Emulator does not validate Account (1)
        [54/68] 7.7   auto     PASS     Duplicate ClOrdID — reject
        [55/68] 7.8   auto     PASS     BusinessMessageReject structure (warning)
        [56/68] 8.1   assisted PASS     Mid-session disconnect: ResendRequest / gap fill on reconnect
        [57/68] 8.2   auto     PASS     PossDup resend of an order is not re-executed
        [58/68] 8.3   auto     PASS     Execution Reports replayed with PossDupFlag=Y
        [59/68] 8.4   auto     PASS     ResendRequest while Execution Reports are in flight
        [60/68] 8.5   auto     PASS     No duplicate ExecIDs on replay
        [61/68] 8.6   auto     PASS     Client restart with sequence reset
        [62/68] 8.7   assisted N/A      Exchange restart: reconnect, gap fill, open orders live — target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
        [63/68] 9.1   manual   PENDING  All required tests passed — needs a human attestation: Review results.json: every required case is PASS (or N/A with the venue's agreement).
        [64/68] 9.2   manual   PENDING  Venue deviations documented — needs a human attestation: Document the venue's deviations from FIX 4.2 seen during certification (warnings and N/A reasons are a starting point).
        [65/68] 9.3   manual   PENDING  Written certification approval — needs a human attestation: Obtain the venue's written certification approval.
        [66/68] 9.4   manual   PENDING  Approval filed internally — needs a human attestation: File the approval email in the internal tracking system with date and venue name.
        [67/68] 9.5   manual   PENDING  Results shared with compliance and operations — needs a human attestation: Share the certification results with compliance and operations.
        [68/68] 9.6   manual   PENDING  Production cutover scheduled — needs a human attestation: Schedule the production cutover date and confirm it with the venue's go-live team.
        
        order-entry-fix42 — Order Entry Certification — FIX 4.2 US Equities
        target orderecho-emulator, session emu42 (FIX.4.2), run 20260929-090316.180, agent 0.3.0 (a3)
        
        ID   TITLE                                             REQ  MODE      STATUS   REASON
        1.1  Certification environment details and documenta…  req  manual    PENDING  needs a human attestation: Confirm the cert host, port and the venue's documentation package were received.
        1.2  TCP connectivity to the cert host                 req  auto      PASS     
        1.3  Outbound firewall permits FIX egress              req  auto      PASS     
        1.4  Inbound rules for a different response IP/port    opt  manual    PENDING  needs a human attestation: Confirm whether the venue pushes responses from a different IP/port range, and that inbound rules allow it (or N/A).
        1.5  CompIDs, password and sequence reset policy col…  req  manual    PENDING  needs a human attestation: Confirm SenderCompID, TargetCompID, session password and the venue's sequence reset policy were provisioned and recorded.
        1.6  TLS vs clear-text requirement confirmed           req  manual    PENDING  needs a human attestation: Confirm with the venue's onboarding team whether TLS is required (this agent speaks clear-text TCP).
        1.7  UAT endpoint confirmed                            req  manual    PENDING  needs a human attestation: Confirm the configured host/port is the venue's UAT (certification) endpoint, not production.
        2.1  BeginString configured                            req  auto      PASS     
        2.2  SenderCompID / TargetCompID as provisioned        req  auto      PASS     
        2.3  HeartBtInt configured                             req  auto      PASS     
        2.4  EncryptMethod = 0                                 req  auto      PASS     
        2.5  ResetOnLogon for the cert session                 opt  auto      PASS     
        2.6  Clean sequence state before the cert              req  auto      PASS     
        2.7  ResetSeqNumFlag=Y accepted on reconnect           req  auto      PASS     
        3.1  Logon acknowledged                                req  auto      PASS     
        3.2  Bidirectional heartbeats at HeartBtInt            req  auto      PASS     
        3.3  TestRequest answered with matching Heartbeat      req  auto      PASS     
        3.4  Exchange-initiated TestRequest answered           req  assisted  PASS     
        3.5  ResendRequest replayed with PossDupFlag=Y         req  auto      PASS     
        3.6  Sequence gap bridged with SequenceReset-GapFill   req  auto      PASS     
        3.7  Graceful Logout acknowledged                      req  auto      PASS     
        3.8  Recovery after a disconnect without Logout        req  auto      PASS     
        4.1  New Order Single — Market Buy                     req  auto      PASS     
        4.2  New Order Single — Market Sell                    req  auto      PASS     
        4.3  New Order Single — Limit Buy                      req  auto      PASS     
        4.4  New Order Single — Limit Sell                     req  auto      PASS     
        4.5  IOC order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.6  FOK order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.7  Day order                                         req  auto      PASS     
        4.8  GTC order persists across session restart         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.9  GTX order                                         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        5.1  Pending New before the acknowledgement            opt  auto      N/A      target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
        5.2  New acknowledgement                               req  auto      PASS     
        5.3  Partial fill                                      req  assisted  PASS     
        5.4  Full fill                                         req  auto      PASS     
        5.5  Done For Day                                      opt  assisted  N/A      target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
        5.6  Expired                                           opt  assisted  N/A      target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
        5.7  ExecIDs unique across the session                 req  auto      PASS     
        5.8  AvgPx and CumQty accumulate across partial fills  req  assisted  PASS     
        6.1  Order Cancel Request references ClOrdID and Ori…  req  auto      PASS     
        6.2  Cancel acknowledgement                            req  auto      PASS     
        6.3  Cancel Reject for a fully-filled order            req  auto      PASS     
        6.4  Cancel Reject — too late to cancel (order pendi…  req  assisted  N/A      target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
        6.5  Cancel/Replace — price only                       req  auto      PASS     
        6.6  Cancel/Replace — quantity up                      req  auto      PASS     
        6.7  Cancel/Replace — quantity down                    opt  auto      PASS     
        6.8  Pending Replace then Replace ack                  opt  auto      N/A      target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
        7.1  Missing required tag — Session Reject             req  auto      PASS     
        7.2  Invalid MsgType — reject                          req  auto      PASS     
        7.3  Invalid Symbol — reject                           req  auto      PASS     
        7.4  Invalid Price — reject                            req  auto      PASS     
        7.5  Invalid Side — reject with RefTagID=54            req  auto      PASS     
        7.6  Invalid Account — reject                          opt  auto      N/A      target orderecho-emulator: Emulator does not validate Account (1)
        7.7  Duplicate ClOrdID — reject                        req  auto      PASS     
        7.8  BusinessMessageReject structure                   req  auto      PASS     warning: step 3 (assert_received): last received message: 35=j seq=66 45=62 372=ZZ 380=3 58=Not supported in this build; recommended tag(s) absent: 3…
        8.1  Mid-session disconnect: ResendRequest / gap fil…  req  assisted  PASS     
        8.2  PossDup resend of an order is not re-executed     req  auto      PASS     
        8.3  Execution Reports replayed with PossDupFlag=Y     req  auto      PASS     
        8.4  ResendRequest while Execution Reports are in fl…  opt  auto      PASS     
        8.5  No duplicate ExecIDs on replay                    req  auto      PASS     
        8.6  Client restart with sequence reset                req  auto      PASS     
        8.7  Exchange restart: reconnect, gap fill, open ord…  opt  assisted  N/A      target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
        9.1  All required tests passed                         req  manual    PENDING  needs a human attestation: Review results.json: every required case is PASS (or N/A with the venue's agreement).
        9.2  Venue deviations documented                       req  manual    PENDING  needs a human attestation: Document the venue's deviations from FIX 4.2 seen during certification (warnings and N/A reasons are a starting point).
        9.3  Written certification approval                    req  manual    PENDING  needs a human attestation: Obtain the venue's written certification approval.
        9.4  Approval filed internally                         req  manual    PENDING  needs a human attestation: File the approval email in the internal tracking system with date and venue name.
        9.5  Results shared with compliance and operations     opt  manual    PENDING  needs a human attestation: Share the certification results with compliance and operations.
        9.6  Production cutover scheduled                      opt  manual    PENDING  needs a human attestation: Schedule the production cutover date and confirm it with the venue's go-live team.
        
        all cases     : PASS 46, PENDING 11, N/A 11
        required cases: PASS 43, PENDING 8, N/A 3
        exit code     : 7
        results       : data/certs/20260929-090316.180
    cert_test.go:156: $ orderecho cert run --suite certs/order_entry_fix42.yaml --target certs/targets/emulator.yaml --session emu42 --attest attest.yaml  -> exit 0
        OrderEcho 0.3.0 (a3) cert run 20260929-090344.212
          suite   : order-entry-fix42 (FIX.4.2, 68 cases) certs/order_entry_fix42.yaml
          target  : orderecho-emulator certs/targets/emulator.yaml
          session : emu42 AGENT -> ORDERECHO @ 127.0.0.1:54941
          attest  : attest.yaml (11 case(s))
        
        [ 1/68] 1.1   manual   PASS     Certification environment details and documentation
        [ 2/68] 1.2   auto     PASS     TCP connectivity to the cert host
        [ 3/68] 1.3   auto     PASS     Outbound firewall permits FIX egress
        [ 4/68] 1.4   manual   N/A      Inbound rules for a different response IP/port — attested na by interop test: same connection
        [ 5/68] 1.5   manual   PASS     CompIDs, password and sequence reset policy collected
        [ 6/68] 1.6   manual   PASS     TLS vs clear-text requirement confirmed
        [ 7/68] 1.7   manual   PASS     UAT endpoint confirmed
        [ 8/68] 2.1   auto     PASS     BeginString configured
        [ 9/68] 2.2   auto     PASS     SenderCompID / TargetCompID as provisioned
        [10/68] 2.3   auto     PASS     HeartBtInt configured
        [11/68] 2.4   auto     PASS     EncryptMethod = 0
        [12/68] 2.5   auto     PASS     ResetOnLogon for the cert session
        [13/68] 2.6   auto     PASS     Clean sequence state before the cert
        [14/68] 2.7   auto     PASS     ResetSeqNumFlag=Y accepted on reconnect
        [15/68] 3.1   auto     PASS     Logon acknowledged
        [16/68] 3.2   auto     PASS     Bidirectional heartbeats at HeartBtInt
        [17/68] 3.3   auto     PASS     TestRequest answered with matching Heartbeat
        [18/68] 3.4   assisted PASS     Exchange-initiated TestRequest answered
        [19/68] 3.5   auto     PASS     ResendRequest replayed with PossDupFlag=Y
        [20/68] 3.6   auto     PASS     Sequence gap bridged with SequenceReset-GapFill
        [21/68] 3.7   auto     PASS     Graceful Logout acknowledged
        [22/68] 3.8   auto     PASS     Recovery after a disconnect without Logout
        [23/68] 4.1   auto     PASS     New Order Single — Market Buy
        [24/68] 4.2   auto     PASS     New Order Single — Market Sell
        [25/68] 4.3   auto     PASS     New Order Single — Limit Buy
        [26/68] 4.4   auto     PASS     New Order Single — Limit Sell
        [27/68] 4.5   auto     N/A      IOC order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [28/68] 4.6   auto     N/A      FOK order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [29/68] 4.7   auto     PASS     Day order
        [30/68] 4.8   auto     N/A      GTC order persists across session restart — target orderecho-emulator: Emulator supports TimeInForce Day only
        [31/68] 4.9   auto     N/A      GTX order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [32/68] 5.1   auto     N/A      Pending New before the acknowledgement — target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
        [33/68] 5.2   auto     PASS     New acknowledgement
        [34/68] 5.3   assisted PASS     Partial fill
        [35/68] 5.4   auto     PASS     Full fill
        [36/68] 5.5   assisted N/A      Done For Day — target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
        [37/68] 5.6   assisted N/A      Expired — target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
        [38/68] 5.7   auto     PASS     ExecIDs unique across the session
        [39/68] 5.8   assisted PASS     AvgPx and CumQty accumulate across partial fills
        [40/68] 6.1   auto     PASS     Order Cancel Request references ClOrdID and OrigClOrdID
        [41/68] 6.2   auto     PASS     Cancel acknowledgement
        [42/68] 6.3   auto     PASS     Cancel Reject for a fully-filled order
        [43/68] 6.4   assisted N/A      Cancel Reject — too late to cancel (order pending fill) — target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
        [44/68] 6.5   auto     PASS     Cancel/Replace — price only
        [45/68] 6.6   auto     PASS     Cancel/Replace — quantity up
        [46/68] 6.7   auto     PASS     Cancel/Replace — quantity down
        [47/68] 6.8   auto     N/A      Pending Replace then Replace ack — target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
        [48/68] 7.1   auto     PASS     Missing required tag — Session Reject
        [49/68] 7.2   auto     PASS     Invalid MsgType — reject
        [50/68] 7.3   auto     PASS     Invalid Symbol — reject
        [51/68] 7.4   auto     PASS     Invalid Price — reject
        [52/68] 7.5   auto     PASS     Invalid Side — reject with RefTagID=54
        [53/68] 7.6   auto     N/A      Invalid Account — reject — target orderecho-emulator: Emulator does not validate Account (1)
        [54/68] 7.7   auto     PASS     Duplicate ClOrdID — reject
        [55/68] 7.8   auto     PASS     BusinessMessageReject structure (warning)
        [56/68] 8.1   assisted PASS     Mid-session disconnect: ResendRequest / gap fill on reconnect
        [57/68] 8.2   auto     PASS     PossDup resend of an order is not re-executed
        [58/68] 8.3   auto     PASS     Execution Reports replayed with PossDupFlag=Y
        [59/68] 8.4   auto     PASS     ResendRequest while Execution Reports are in flight
        [60/68] 8.5   auto     PASS     No duplicate ExecIDs on replay
        [61/68] 8.6   auto     PASS     Client restart with sequence reset
        [62/68] 8.7   assisted N/A      Exchange restart: reconnect, gap fill, open orders live — target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
        [63/68] 9.1   manual   PASS     All required tests passed
        [64/68] 9.2   manual   PASS     Venue deviations documented
        [65/68] 9.3   manual   PASS     Written certification approval
        [66/68] 9.4   manual   PASS     Approval filed internally
        [67/68] 9.5   manual   N/A      Results shared with compliance and operations — attested na by interop test: test
        [68/68] 9.6   manual   N/A      Production cutover scheduled — attested na by interop test: test
        
        order-entry-fix42 — Order Entry Certification — FIX 4.2 US Equities
        target orderecho-emulator, session emu42 (FIX.4.2), run 20260929-090344.212, agent 0.3.0 (a3)
        
        ID   TITLE                                             REQ  MODE      STATUS  REASON
        1.1  Certification environment details and documenta…  req  manual    PASS    attested pass by interop test: emulator is local
        1.2  TCP connectivity to the cert host                 req  auto      PASS    
        1.3  Outbound firewall permits FIX egress              req  auto      PASS    
        1.4  Inbound rules for a different response IP/port    opt  manual    N/A     attested na by interop test: same connection
        1.5  CompIDs, password and sequence reset policy col…  req  manual    PASS    attested pass by interop test: AGENT/ORDERECHO
        1.6  TLS vs clear-text requirement confirmed           req  manual    PASS    attested pass by interop test: clear-text loopback
        1.7  UAT endpoint confirmed                            req  manual    PASS    attested pass by interop test: emulator = UAT
        2.1  BeginString configured                            req  auto      PASS    
        2.2  SenderCompID / TargetCompID as provisioned        req  auto      PASS    
        2.3  HeartBtInt configured                             req  auto      PASS    
        2.4  EncryptMethod = 0                                 req  auto      PASS    
        2.5  ResetOnLogon for the cert session                 opt  auto      PASS    
        2.6  Clean sequence state before the cert              req  auto      PASS    
        2.7  ResetSeqNumFlag=Y accepted on reconnect           req  auto      PASS    
        3.1  Logon acknowledged                                req  auto      PASS    
        3.2  Bidirectional heartbeats at HeartBtInt            req  auto      PASS    
        3.3  TestRequest answered with matching Heartbeat      req  auto      PASS    
        3.4  Exchange-initiated TestRequest answered           req  assisted  PASS    
        3.5  ResendRequest replayed with PossDupFlag=Y         req  auto      PASS    
        3.6  Sequence gap bridged with SequenceReset-GapFill   req  auto      PASS    
        3.7  Graceful Logout acknowledged                      req  auto      PASS    
        3.8  Recovery after a disconnect without Logout        req  auto      PASS    
        4.1  New Order Single — Market Buy                     req  auto      PASS    
        4.2  New Order Single — Market Sell                    req  auto      PASS    
        4.3  New Order Single — Limit Buy                      req  auto      PASS    
        4.4  New Order Single — Limit Sell                     req  auto      PASS    
        4.5  IOC order                                         req  auto      N/A     target orderecho-emulator: Emulator supports TimeInForce Day only
        4.6  FOK order                                         req  auto      N/A     target orderecho-emulator: Emulator supports TimeInForce Day only
        4.7  Day order                                         req  auto      PASS    
        4.8  GTC order persists across session restart         opt  auto      N/A     target orderecho-emulator: Emulator supports TimeInForce Day only
        4.9  GTX order                                         opt  auto      N/A     target orderecho-emulator: Emulator supports TimeInForce Day only
        5.1  Pending New before the acknowledgement            opt  auto      N/A     target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
        5.2  New acknowledgement                               req  auto      PASS    
        5.3  Partial fill                                      req  assisted  PASS    
        5.4  Full fill                                         req  auto      PASS    
        5.5  Done For Day                                      opt  assisted  N/A     target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
        5.6  Expired                                           opt  assisted  N/A     target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
        5.7  ExecIDs unique across the session                 req  auto      PASS    
        5.8  AvgPx and CumQty accumulate across partial fills  req  assisted  PASS    
        6.1  Order Cancel Request references ClOrdID and Ori…  req  auto      PASS    
        6.2  Cancel acknowledgement                            req  auto      PASS    
        6.3  Cancel Reject for a fully-filled order            req  auto      PASS    
        6.4  Cancel Reject — too late to cancel (order pendi…  req  assisted  N/A     target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
        6.5  Cancel/Replace — price only                       req  auto      PASS    
        6.6  Cancel/Replace — quantity up                      req  auto      PASS    
        6.7  Cancel/Replace — quantity down                    opt  auto      PASS    
        6.8  Pending Replace then Replace ack                  opt  auto      N/A     target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
        7.1  Missing required tag — Session Reject             req  auto      PASS    
        7.2  Invalid MsgType — reject                          req  auto      PASS    
        7.3  Invalid Symbol — reject                           req  auto      PASS    
        7.4  Invalid Price — reject                            req  auto      PASS    
        7.5  Invalid Side — reject with RefTagID=54            req  auto      PASS    
        7.6  Invalid Account — reject                          opt  auto      N/A     target orderecho-emulator: Emulator does not validate Account (1)
        7.7  Duplicate ClOrdID — reject                        req  auto      PASS    
        7.8  BusinessMessageReject structure                   req  auto      PASS    warning: step 3 (assert_received): last received message: 35=j seq=66 45=62 372=ZZ 380=3 58=Not supported in this build; recommended tag(s) absent: 3…
        8.1  Mid-session disconnect: ResendRequest / gap fil…  req  assisted  PASS    
        8.2  PossDup resend of an order is not re-executed     req  auto      PASS    
        8.3  Execution Reports replayed with PossDupFlag=Y     req  auto      PASS    
        8.4  ResendRequest while Execution Reports are in fl…  opt  auto      PASS    
        8.5  No duplicate ExecIDs on replay                    req  auto      PASS    
        8.6  Client restart with sequence reset                req  auto      PASS    
        8.7  Exchange restart: reconnect, gap fill, open ord…  opt  assisted  N/A     target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
        9.1  All required tests passed                         req  manual    PASS    attested pass by interop test: reviewed
        9.2  Venue deviations documented                       req  manual    PASS    attested pass by interop test: N/A reasons
        9.3  Written certification approval                    req  manual    PASS    attested pass by interop test: simulated
        9.4  Approval filed internally                         req  manual    PASS    attested pass by interop test: simulated
        9.5  Results shared with compliance and operations     opt  manual    N/A     attested na by interop test: test
        9.6  Production cutover scheduled                      opt  manual    N/A     attested na by interop test: test
        
        all cases     : PASS 54, N/A 14
        required cases: PASS 51, N/A 3
        exit code     : 0
        results       : data/certs/20260929-090344.212
--- PASS: TestCertEmulatorFIX42 (68.96s)
=== RUN   TestCertEmulatorFIX44
    cert_test.go:171: emulator up: fix=55088 strict=55089 api=55090 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCertEmulatorFIX442793272103/001/emulator
    cert_test.go:173: $ orderecho cert run --suite certs/order_entry_fix44.yaml --target certs/targets/emulator.yaml --session emu44  -> exit 7
        OrderEcho 0.3.0 (a3) cert run 20260929-090423.125
          suite   : order-entry-fix44 (FIX.4.4, 68 cases) certs/order_entry_fix44.yaml
          target  : orderecho-emulator certs/targets/emulator.yaml
          session : emu44 AGENT -> ORDERECHO @ 127.0.0.1:55088
        
        [ 1/68] 1.1   manual   PENDING  Certification environment details and documentation — needs a human attestation: Confirm the cert host, port and the venue's documentation package were received.
        [ 2/68] 1.2   auto     PASS     TCP connectivity to the cert host
        [ 3/68] 1.3   auto     PASS     Outbound firewall permits FIX egress
        [ 4/68] 1.4   manual   PENDING  Inbound rules for a different response IP/port — needs a human attestation: Confirm whether the venue pushes responses from a different IP/port range, and that inbound rules allow it (or N/A).
        [ 5/68] 1.5   manual   PENDING  CompIDs, password and sequence reset policy collected — needs a human attestation: Confirm SenderCompID, TargetCompID, session password and the venue's sequence reset policy were provisioned and recorded.
        [ 6/68] 1.6   manual   PENDING  TLS vs clear-text requirement confirmed — needs a human attestation: Confirm with the venue's onboarding team whether TLS is required (this agent speaks clear-text TCP).
        [ 7/68] 1.7   manual   PENDING  UAT endpoint confirmed — needs a human attestation: Confirm the configured host/port is the venue's UAT (certification) endpoint, not production.
        [ 8/68] 2.1   auto     PASS     BeginString configured
        [ 9/68] 2.2   auto     PASS     SenderCompID / TargetCompID as provisioned
        [10/68] 2.3   auto     PASS     HeartBtInt configured
        [11/68] 2.4   auto     PASS     EncryptMethod = 0
        [12/68] 2.5   auto     PASS     ResetOnLogon for the cert session
        [13/68] 2.6   auto     PASS     Clean sequence state before the cert
        [14/68] 2.7   auto     PASS     ResetSeqNumFlag=Y accepted on reconnect
        [15/68] 3.1   auto     PASS     Logon acknowledged
        [16/68] 3.2   auto     PASS     Bidirectional heartbeats at HeartBtInt
        [17/68] 3.3   auto     PASS     TestRequest answered with matching Heartbeat
        [18/68] 3.4   assisted PASS     Exchange-initiated TestRequest answered
        [19/68] 3.5   auto     PASS     ResendRequest replayed with PossDupFlag=Y
        [20/68] 3.6   auto     PASS     Sequence gap bridged with SequenceReset-GapFill
        [21/68] 3.7   auto     PASS     Graceful Logout acknowledged
        [22/68] 3.8   auto     PASS     Recovery after a disconnect without Logout
        [23/68] 4.1   auto     PASS     New Order Single — Market Buy
        [24/68] 4.2   auto     PASS     New Order Single — Market Sell
        [25/68] 4.3   auto     PASS     New Order Single — Limit Buy
        [26/68] 4.4   auto     PASS     New Order Single — Limit Sell
        [27/68] 4.5   auto     N/A      IOC order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [28/68] 4.6   auto     N/A      FOK order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [29/68] 4.7   auto     PASS     Day order
        [30/68] 4.8   auto     N/A      GTC order persists across session restart — target orderecho-emulator: Emulator supports TimeInForce Day only
        [31/68] 4.9   auto     N/A      GTX order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [32/68] 5.1   auto     N/A      Pending New before the acknowledgement — target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
        [33/68] 5.2   auto     PASS     New acknowledgement
        [34/68] 5.3   assisted PASS     Partial fill
        [35/68] 5.4   auto     PASS     Full fill
        [36/68] 5.5   assisted N/A      Done For Day — target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
        [37/68] 5.6   assisted N/A      Expired — target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
        [38/68] 5.7   auto     PASS     ExecIDs unique across the session
        [39/68] 5.8   assisted PASS     AvgPx and CumQty accumulate across partial fills
        [40/68] 6.1   auto     PASS     Order Cancel Request references ClOrdID and OrigClOrdID
        [41/68] 6.2   auto     PASS     Cancel acknowledgement
        [42/68] 6.3   auto     PASS     Cancel Reject for a fully-filled order
        [43/68] 6.4   assisted N/A      Cancel Reject — too late to cancel (order pending fill) — target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
        [44/68] 6.5   auto     PASS     Cancel/Replace — price only
        [45/68] 6.6   auto     PASS     Cancel/Replace — quantity up
        [46/68] 6.7   auto     PASS     Cancel/Replace — quantity down
        [47/68] 6.8   auto     N/A      Pending Replace then Replace ack — target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
        [48/68] 7.1   auto     PASS     Missing required tag — Session Reject
        [49/68] 7.2   auto     PASS     Invalid MsgType — reject
        [50/68] 7.3   auto     PASS     Invalid Symbol — reject
        [51/68] 7.4   auto     PASS     Invalid Price — reject
        [52/68] 7.5   auto     PASS     Invalid Side — reject with RefTagID=54
        [53/68] 7.6   auto     N/A      Invalid Account — reject — target orderecho-emulator: Emulator does not validate Account (1)
        [54/68] 7.7   auto     PASS     Duplicate ClOrdID — reject
        [55/68] 7.8   auto     PASS     BusinessMessageReject structure (warning)
        [56/68] 8.1   assisted PASS     Mid-session disconnect: ResendRequest / gap fill on reconnect
        [57/68] 8.2   auto     PASS     PossDup resend of an order is not re-executed
        [58/68] 8.3   auto     PASS     Execution Reports replayed with PossDupFlag=Y
        [59/68] 8.4   auto     PASS     ResendRequest while Execution Reports are in flight
        [60/68] 8.5   auto     PASS     No duplicate ExecIDs on replay
        [61/68] 8.6   auto     PASS     Client restart with sequence reset
        [62/68] 8.7   assisted N/A      Exchange restart: reconnect, gap fill, open orders live — target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
        [63/68] 9.1   manual   PENDING  All required tests passed — needs a human attestation: Review results.json: every required case is PASS (or N/A with the venue's agreement).
        [64/68] 9.2   manual   PENDING  Venue deviations documented — needs a human attestation: Document the venue's deviations from FIX 4.2 seen during certification (warnings and N/A reasons are a starting point).
        [65/68] 9.3   manual   PENDING  Written certification approval — needs a human attestation: Obtain the venue's written certification approval.
        [66/68] 9.4   manual   PENDING  Approval filed internally — needs a human attestation: File the approval email in the internal tracking system with date and venue name.
        [67/68] 9.5   manual   PENDING  Results shared with compliance and operations — needs a human attestation: Share the certification results with compliance and operations.
        [68/68] 9.6   manual   PENDING  Production cutover scheduled — needs a human attestation: Schedule the production cutover date and confirm it with the venue's go-live team.
        
        order-entry-fix44 — Order Entry Certification — FIX 4.4 US Equities (4.2 checklist)
        target orderecho-emulator, session emu44 (FIX.4.4), run 20260929-090423.125, agent 0.3.0 (a3)
        
        ID   TITLE                                             REQ  MODE      STATUS   REASON
        1.1  Certification environment details and documenta…  req  manual    PENDING  needs a human attestation: Confirm the cert host, port and the venue's documentation package were received.
        1.2  TCP connectivity to the cert host                 req  auto      PASS     
        1.3  Outbound firewall permits FIX egress              req  auto      PASS     
        1.4  Inbound rules for a different response IP/port    opt  manual    PENDING  needs a human attestation: Confirm whether the venue pushes responses from a different IP/port range, and that inbound rules allow it (or N/A).
        1.5  CompIDs, password and sequence reset policy col…  req  manual    PENDING  needs a human attestation: Confirm SenderCompID, TargetCompID, session password and the venue's sequence reset policy were provisioned and recorded.
        1.6  TLS vs clear-text requirement confirmed           req  manual    PENDING  needs a human attestation: Confirm with the venue's onboarding team whether TLS is required (this agent speaks clear-text TCP).
        1.7  UAT endpoint confirmed                            req  manual    PENDING  needs a human attestation: Confirm the configured host/port is the venue's UAT (certification) endpoint, not production.
        2.1  BeginString configured                            req  auto      PASS     
        2.2  SenderCompID / TargetCompID as provisioned        req  auto      PASS     
        2.3  HeartBtInt configured                             req  auto      PASS     
        2.4  EncryptMethod = 0                                 req  auto      PASS     
        2.5  ResetOnLogon for the cert session                 opt  auto      PASS     
        2.6  Clean sequence state before the cert              req  auto      PASS     
        2.7  ResetSeqNumFlag=Y accepted on reconnect           req  auto      PASS     
        3.1  Logon acknowledged                                req  auto      PASS     
        3.2  Bidirectional heartbeats at HeartBtInt            req  auto      PASS     
        3.3  TestRequest answered with matching Heartbeat      req  auto      PASS     
        3.4  Exchange-initiated TestRequest answered           req  assisted  PASS     
        3.5  ResendRequest replayed with PossDupFlag=Y         req  auto      PASS     
        3.6  Sequence gap bridged with SequenceReset-GapFill   req  auto      PASS     
        3.7  Graceful Logout acknowledged                      req  auto      PASS     
        3.8  Recovery after a disconnect without Logout        req  auto      PASS     
        4.1  New Order Single — Market Buy                     req  auto      PASS     
        4.2  New Order Single — Market Sell                    req  auto      PASS     
        4.3  New Order Single — Limit Buy                      req  auto      PASS     
        4.4  New Order Single — Limit Sell                     req  auto      PASS     
        4.5  IOC order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.6  FOK order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.7  Day order                                         req  auto      PASS     
        4.8  GTC order persists across session restart         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.9  GTX order                                         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        5.1  Pending New before the acknowledgement            opt  auto      N/A      target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
        5.2  New acknowledgement                               req  auto      PASS     
        5.3  Partial fill                                      req  assisted  PASS     
        5.4  Full fill                                         req  auto      PASS     
        5.5  Done For Day                                      opt  assisted  N/A      target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
        5.6  Expired                                           opt  assisted  N/A      target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
        5.7  ExecIDs unique across the session                 req  auto      PASS     
        5.8  AvgPx and CumQty accumulate across partial fills  req  assisted  PASS     
        6.1  Order Cancel Request references ClOrdID and Ori…  req  auto      PASS     
        6.2  Cancel acknowledgement                            req  auto      PASS     
        6.3  Cancel Reject for a fully-filled order            req  auto      PASS     
        6.4  Cancel Reject — too late to cancel (order pendi…  req  assisted  N/A      target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
        6.5  Cancel/Replace — price only                       req  auto      PASS     
        6.6  Cancel/Replace — quantity up                      req  auto      PASS     
        6.7  Cancel/Replace — quantity down                    opt  auto      PASS     
        6.8  Pending Replace then Replace ack                  opt  auto      N/A      target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
        7.1  Missing required tag — Session Reject             req  auto      PASS     
        7.2  Invalid MsgType — reject                          req  auto      PASS     
        7.3  Invalid Symbol — reject                           req  auto      PASS     
        7.4  Invalid Price — reject                            req  auto      PASS     
        7.5  Invalid Side — reject with RefTagID=54            req  auto      PASS     
        7.6  Invalid Account — reject                          opt  auto      N/A      target orderecho-emulator: Emulator does not validate Account (1)
        7.7  Duplicate ClOrdID — reject                        req  auto      PASS     
        7.8  BusinessMessageReject structure                   req  auto      PASS     warning: step 3 (assert_received): last received message: 35=j seq=66 45=62 372=ZZ 380=3 58=Not supported in this build; recommended tag(s) absent: 3…
        8.1  Mid-session disconnect: ResendRequest / gap fil…  req  assisted  PASS     
        8.2  PossDup resend of an order is not re-executed     req  auto      PASS     
        8.3  Execution Reports replayed with PossDupFlag=Y     req  auto      PASS     
        8.4  ResendRequest while Execution Reports are in fl…  opt  auto      PASS     
        8.5  No duplicate ExecIDs on replay                    req  auto      PASS     
        8.6  Client restart with sequence reset                req  auto      PASS     
        8.7  Exchange restart: reconnect, gap fill, open ord…  opt  assisted  N/A      target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
        9.1  All required tests passed                         req  manual    PENDING  needs a human attestation: Review results.json: every required case is PASS (or N/A with the venue's agreement).
        9.2  Venue deviations documented                       req  manual    PENDING  needs a human attestation: Document the venue's deviations from FIX 4.2 seen during certification (warnings and N/A reasons are a starting point).
        9.3  Written certification approval                    req  manual    PENDING  needs a human attestation: Obtain the venue's written certification approval.
        9.4  Approval filed internally                         req  manual    PENDING  needs a human attestation: File the approval email in the internal tracking system with date and venue name.
        9.5  Results shared with compliance and operations     opt  manual    PENDING  needs a human attestation: Share the certification results with compliance and operations.
        9.6  Production cutover scheduled                      opt  manual    PENDING  needs a human attestation: Schedule the production cutover date and confirm it with the venue's go-live team.
        
        all cases     : PASS 46, PENDING 11, N/A 11
        required cases: PASS 43, PENDING 8, N/A 3
        exit code     : 7
        results       : data/certs/20260929-090423.125
    cert_test.go:183: $ orderecho cert run --suite certs/order_entry_fix42.yaml --target certs/targets/emulator.yaml --session emu44  -> exit 2
        orderecho: suite order-entry-fix42 is FIX.4.2 but session emu44 is FIX.4.4 (use the matching suite, e.g. order_entry_fix44.yaml)
--- PASS: TestCertEmulatorFIX44 (39.37s)
=== RUN   TestCertStrictBrokerNegativeControl
    cert_test.go:190: emulator up: fix=55207 strict=55208 api=55209 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCertStrictBrokerNegativeControl1782748988/001/emulator
    cert_test.go:192: $ orderecho cert run --suite certs/order_entry_fix42.yaml --target certs/targets/emulator.yaml --session strict  -> exit 5
        OrderEcho 0.3.0 (a3) cert run 20260929-090504.952
          suite   : order-entry-fix42 (FIX.4.2, 68 cases) certs/order_entry_fix42.yaml
          target  : orderecho-emulator certs/targets/emulator.yaml
          session : strict AGENT -> STRICTBRK @ 127.0.0.1:55208
        
        [ 1/68] 1.1   manual   PENDING  Certification environment details and documentation — needs a human attestation: Confirm the cert host, port and the venue's documentation package were received.
        [ 2/68] 1.2   auto     PASS     TCP connectivity to the cert host
        [ 3/68] 1.3   auto     PASS     Outbound firewall permits FIX egress
        [ 4/68] 1.4   manual   PENDING  Inbound rules for a different response IP/port — needs a human attestation: Confirm whether the venue pushes responses from a different IP/port range, and that inbound rules allow it (or N/A).
        [ 5/68] 1.5   manual   PENDING  CompIDs, password and sequence reset policy collected — needs a human attestation: Confirm SenderCompID, TargetCompID, session password and the venue's sequence reset policy were provisioned and recorded.
        [ 6/68] 1.6   manual   PENDING  TLS vs clear-text requirement confirmed — needs a human attestation: Confirm with the venue's onboarding team whether TLS is required (this agent speaks clear-text TCP).
        [ 7/68] 1.7   manual   PENDING  UAT endpoint confirmed — needs a human attestation: Confirm the configured host/port is the venue's UAT (certification) endpoint, not production.
        [ 8/68] 2.1   auto     PASS     BeginString configured
        [ 9/68] 2.2   auto     PASS     SenderCompID / TargetCompID as provisioned
        [10/68] 2.3   auto     PASS     HeartBtInt configured
        [11/68] 2.4   auto     PASS     EncryptMethod = 0
        [12/68] 2.5   auto     PASS     ResetOnLogon for the cert session
        [13/68] 2.6   auto     PASS     Clean sequence state before the cert
        [14/68] 2.7   auto     PASS     ResetSeqNumFlag=Y accepted on reconnect
        [15/68] 3.1   auto     PASS     Logon acknowledged
        [16/68] 3.2   auto     PASS     Bidirectional heartbeats at HeartBtInt
        [17/68] 3.3   auto     PASS     TestRequest answered with matching Heartbeat
        [18/68] 3.4   assisted PASS     Exchange-initiated TestRequest answered
        [19/68] 3.5   auto     FAIL     ResendRequest replayed with PossDupFlag=Y — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=8 11=OE-20260929-090504.952-1 37=O-2026092…
        [20/68] 3.6   auto     PASS     Sequence gap bridged with SequenceReset-GapFill
        [21/68] 3.7   auto     PASS     Graceful Logout acknowledged
        [22/68] 3.8   auto     PASS     Recovery after a disconnect without Logout
        [23/68] 4.1   auto     FAIL     New Order Single — Market Buy — step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=16 11=OE-20260929-090504.95…
        [24/68] 4.2   auto     FAIL     New Order Single — Market Sell — step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=17 11=OE-20260929-090504.95…
        [25/68] 4.3   auto     FAIL     New Order Single — Limit Buy — step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=18 11=OE-20260929-090504.95…
        [26/68] 4.4   auto     FAIL     New Order Single — Limit Sell — step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=19 11=OE-20260929-090504.95…
        [27/68] 4.5   auto     N/A      IOC order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [28/68] 4.6   auto     N/A      FOK order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [29/68] 4.7   auto     FAIL     Day order — step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=20 11=OE-20260929-090504.95…
        [30/68] 4.8   auto     N/A      GTC order persists across session restart — target orderecho-emulator: Emulator supports TimeInForce Day only
        [31/68] 4.9   auto     N/A      GTX order — target orderecho-emulator: Emulator supports TimeInForce Day only
        [32/68] 5.1   auto     N/A      Pending New before the acknowledgement — target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
        [33/68] 5.2   auto     FAIL     New acknowledgement — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW 14=0 151=100 present [37 17] from the counterparty: 35=8 se…
        [34/68] 5.3   assisted FAIL     Partial fill — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=22 11=OE-20260929-090504.952-8 37=O-202609…
        [35/68] 5.4   auto     FAIL     Full fill — step 2 (expect): the counterparty rejected the order while waiting for exec_type=FILL ord_status=FILLED 14=100 151=0 from the counterparty: 35=8 seq=23 11=OE-2…
        [36/68] 5.5   assisted N/A      Done For Day — target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
        [37/68] 5.6   assisted N/A      Expired — target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
        [38/68] 5.7   auto     FAIL     ExecIDs unique across the session — step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=24 11=OE-20260929-090504.952-10 37=O-2…
        [39/68] 5.8   assisted FAIL     AvgPx and CumQty accumulate across partial fills — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=25 11=OE-20260929-090504.952-11 37=O-20260…
        [40/68] 6.1   auto     FAIL     Order Cancel Request references ClOrdID and OrigClOrdID — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=26 11=OE-20260929-090504.952-12 37=O-20260…
        [41/68] 6.2   auto     FAIL     Cancel acknowledgement — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=27 11=OE-20260929-090504.952-13 37=O-20260…
        [42/68] 6.3   auto     FAIL     Cancel Reject for a fully-filled order — step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=28 11=OE-20260929-090504.952-14 37=O-2…
        [43/68] 6.4   assisted N/A      Cancel Reject — too late to cancel (order pending fill) — target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
        [44/68] 6.5   auto     FAIL     Cancel/Replace — price only — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=29 11=OE-20260929-090504.952-15 37=O-20260…
        [45/68] 6.6   auto     FAIL     Cancel/Replace — quantity up — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=30 11=OE-20260929-090504.952-16 37=O-20260…
        [46/68] 6.7   auto     FAIL     Cancel/Replace — quantity down — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=31 11=OE-20260929-090504.952-17 37=O-20260…
        [47/68] 6.8   auto     N/A      Pending Replace then Replace ack — target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
        [48/68] 7.1   auto     PASS     Missing required tag — Session Reject
        [49/68] 7.2   auto     PASS     Invalid MsgType — reject
        [50/68] 7.3   auto     PASS     Invalid Symbol — reject
        [51/68] 7.4   auto     PASS     Invalid Price — reject
        [52/68] 7.5   auto     PASS     Invalid Side — reject with RefTagID=54
        [53/68] 7.6   auto     N/A      Invalid Account — reject — target orderecho-emulator: Emulator does not validate Account (1)
        [54/68] 7.7   auto     FAIL     Duplicate ClOrdID — reject — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=37 11=OE-20260929-090504.952-19 37=O-20260…
        [55/68] 7.8   auto     PASS     BusinessMessageReject structure (warning)
        [56/68] 8.1   assisted PASS     Mid-session disconnect: ResendRequest / gap fill on reconnect
        [57/68] 8.2   auto     FAIL     PossDup resend of an order is not re-executed — step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=44 11=OE-20260929-090504.952-20 37=O-20260…
        [58/68] 8.3   auto     FAIL     Execution Reports replayed with PossDupFlag=Y — step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=45 11=OE-20260929-090504.952-21 37=O-2…
        [59/68] 8.4   auto     FAIL     ResendRequest while Execution Reports are in flight — step 3 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=46 11=OE-20260929-090504.952-22 37=O-2…
        [60/68] 8.5   auto     FAIL     No duplicate ExecIDs on replay — step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=47 11=OE-20260929-090504.952-23 37=O-2…
        [61/68] 8.6   auto     PASS     Client restart with sequence reset
        [62/68] 8.7   assisted N/A      Exchange restart: reconnect, gap fill, open orders live — target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
        [63/68] 9.1   manual   PENDING  All required tests passed — needs a human attestation: Review results.json: every required case is PASS (or N/A with the venue's agreement).
        [64/68] 9.2   manual   PENDING  Venue deviations documented — needs a human attestation: Document the venue's deviations from FIX 4.2 seen during certification (warnings and N/A reasons are a starting point).
        [65/68] 9.3   manual   PENDING  Written certification approval — needs a human attestation: Obtain the venue's written certification approval.
        [66/68] 9.4   manual   PENDING  Approval filed internally — needs a human attestation: File the approval email in the internal tracking system with date and venue name.
        [67/68] 9.5   manual   PENDING  Results shared with compliance and operations — needs a human attestation: Share the certification results with compliance and operations.
        [68/68] 9.6   manual   PENDING  Production cutover scheduled — needs a human attestation: Schedule the production cutover date and confirm it with the venue's go-live team.
        
        order-entry-fix42 — Order Entry Certification — FIX 4.2 US Equities
        target orderecho-emulator, session strict (FIX.4.2), run 20260929-090504.952, agent 0.3.0 (a3)
        
        ID   TITLE                                             REQ  MODE      STATUS   REASON
        1.1  Certification environment details and documenta…  req  manual    PENDING  needs a human attestation: Confirm the cert host, port and the venue's documentation package were received.
        1.2  TCP connectivity to the cert host                 req  auto      PASS     
        1.3  Outbound firewall permits FIX egress              req  auto      PASS     
        1.4  Inbound rules for a different response IP/port    opt  manual    PENDING  needs a human attestation: Confirm whether the venue pushes responses from a different IP/port range, and that inbound rules allow it (or N/A).
        1.5  CompIDs, password and sequence reset policy col…  req  manual    PENDING  needs a human attestation: Confirm SenderCompID, TargetCompID, session password and the venue's sequence reset policy were provisioned and recorded.
        1.6  TLS vs clear-text requirement confirmed           req  manual    PENDING  needs a human attestation: Confirm with the venue's onboarding team whether TLS is required (this agent speaks clear-text TCP).
        1.7  UAT endpoint confirmed                            req  manual    PENDING  needs a human attestation: Confirm the configured host/port is the venue's UAT (certification) endpoint, not production.
        2.1  BeginString configured                            req  auto      PASS     
        2.2  SenderCompID / TargetCompID as provisioned        req  auto      PASS     
        2.3  HeartBtInt configured                             req  auto      PASS     
        2.4  EncryptMethod = 0                                 req  auto      PASS     
        2.5  ResetOnLogon for the cert session                 opt  auto      PASS     
        2.6  Clean sequence state before the cert              req  auto      PASS     
        2.7  ResetSeqNumFlag=Y accepted on reconnect           req  auto      PASS     
        3.1  Logon acknowledged                                req  auto      PASS     
        3.2  Bidirectional heartbeats at HeartBtInt            req  auto      PASS     
        3.3  TestRequest answered with matching Heartbeat      req  auto      PASS     
        3.4  Exchange-initiated TestRequest answered           req  assisted  PASS     
        3.5  ResendRequest replayed with PossDupFlag=Y         req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=8 11=OE-20260929-090504.952-1 37…
        3.6  Sequence gap bridged with SequenceReset-GapFill   req  auto      PASS     
        3.7  Graceful Logout acknowledged                      req  auto      PASS     
        3.8  Recovery after a disconnect without Logout        req  auto      PASS     
        4.1  New Order Single — Market Buy                     req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=16 11=OE-20260929…
        4.2  New Order Single — Market Sell                    req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=17 11=OE-20260929…
        4.3  New Order Single — Limit Buy                      req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=18 11=OE-20260929…
        4.4  New Order Single — Limit Sell                     req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=19 11=OE-20260929…
        4.5  IOC order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.6  FOK order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.7  Day order                                         req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=20 11=OE-20260929…
        4.8  GTC order persists across session restart         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        4.9  GTX order                                         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
        5.1  Pending New before the acknowledgement            opt  auto      N/A      target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
        5.2  New acknowledgement                               req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW 14=0 151=100 present [37 17] from the counterpart…
        5.3  Partial fill                                      req  assisted  FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=22 11=OE-20260929-090504.952-8 3…
        5.4  Full fill                                         req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=FILL ord_status=FILLED 14=100 151=0 from the counterparty: 35=8 seq=…
        5.5  Done For Day                                      opt  assisted  N/A      target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
        5.6  Expired                                           opt  assisted  N/A      target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
        5.7  ExecIDs unique across the session                 req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=24 11=OE-20260929-090504.952…
        5.8  AvgPx and CumQty accumulate across partial fills  req  assisted  FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=25 11=OE-20260929-090504.952-11 …
        6.1  Order Cancel Request references ClOrdID and Ori…  req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=26 11=OE-20260929-090504.952-12 …
        6.2  Cancel acknowledgement                            req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=27 11=OE-20260929-090504.952-13 …
        6.3  Cancel Reject for a fully-filled order            req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=28 11=OE-20260929-090504.952…
        6.4  Cancel Reject — too late to cancel (order pendi…  req  assisted  N/A      target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
        6.5  Cancel/Replace — price only                       req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=29 11=OE-20260929-090504.952-15 …
        6.6  Cancel/Replace — quantity up                      req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=30 11=OE-20260929-090504.952-16 …
        6.7  Cancel/Replace — quantity down                    opt  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=31 11=OE-20260929-090504.952-17 …
        6.8  Pending Replace then Replace ack                  opt  auto      N/A      target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
        7.1  Missing required tag — Session Reject             req  auto      PASS     
        7.2  Invalid MsgType — reject                          req  auto      PASS     
        7.3  Invalid Symbol — reject                           req  auto      PASS     
        7.4  Invalid Price — reject                            req  auto      PASS     
        7.5  Invalid Side — reject with RefTagID=54            req  auto      PASS     
        7.6  Invalid Account — reject                          opt  auto      N/A      target orderecho-emulator: Emulator does not validate Account (1)
        7.7  Duplicate ClOrdID — reject                        req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=37 11=OE-20260929-090504.952-19 …
        7.8  BusinessMessageReject structure                   req  auto      PASS     warning: step 3 (assert_received): last received message: 35=j seq=38 45=42 372=ZZ 380=3 58=Not supported in this build; recommended tag(s) absent: 3…
        8.1  Mid-session disconnect: ResendRequest / gap fil…  req  assisted  PASS     
        8.2  PossDup resend of an order is not re-executed     req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=44 11=OE-20260929-090504.952-20 …
        8.3  Execution Reports replayed with PossDupFlag=Y     req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=45 11=OE-20260929-090504.952…
        8.4  ResendRequest while Execution Reports are in fl…  opt  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=46 11=OE-20260929-090504.952…
        8.5  No duplicate ExecIDs on replay                    req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=47 11=OE-20260929-090504.952…
        8.6  Client restart with sequence reset                req  auto      PASS     
        8.7  Exchange restart: reconnect, gap fill, open ord…  opt  assisted  N/A      target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
        9.1  All required tests passed                         req  manual    PENDING  needs a human attestation: Review results.json: every required case is PASS (or N/A with the venue's agreement).
        9.2  Venue deviations documented                       req  manual    PENDING  needs a human attestation: Document the venue's deviations from FIX 4.2 seen during certification (warnings and N/A reasons are a starting point).
        9.3  Written certification approval                    req  manual    PENDING  needs a human attestation: Obtain the venue's written certification approval.
        9.4  Approval filed internally                         req  manual    PENDING  needs a human attestation: File the approval email in the internal tracking system with date and venue name.
        9.5  Results shared with compliance and operations     opt  manual    PENDING  needs a human attestation: Share the certification results with compliance and operations.
        9.6  Production cutover scheduled                      opt  manual    PENDING  needs a human attestation: Schedule the production cutover date and confirm it with the venue's go-live team.
        
        all cases     : PASS 24, FAIL 22, PENDING 11, N/A 11
        required cases: PASS 23, FAIL 20, PENDING 8, N/A 3
        exit code     : 5
        results       : data/certs/20260929-090504.952
--- PASS: TestCertStrictBrokerNegativeControl (32.39s)
=== RUN   TestCertGenericTargetBlocksAssisted
    cert_test.go:224: emulator up: fix=55347 strict=55348 api=55349 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCertGenericTargetBlocksAssisted784958559/001/emulator
    cert_test.go:226: $ orderecho cert run --suite certs/order_entry_fix42.yaml --target certs/targets/generic.yaml --session emu42 --case 3.4,5.3,5.5,5.6,5.8,6.4,8.1,8.7,4.1 --var hold_symbol=ZWZZT --var limit_symbol=ZWZZT  -> exit 7
        OrderEcho 0.3.0 (a3) cert run 20260929-090536.042
          suite   : order-entry-fix42 (FIX.4.2, 68 cases) certs/order_entry_fix42.yaml
          target  : generic certs/targets/generic.yaml
          session : emu42 AGENT -> ORDERECHO @ 127.0.0.1:55347
        
        [ 1/9] 3.4   assisted BLOCKED  Exchange-initiated TestRequest answered — BLOCKED: needs counterparty action: the counterparty sends a TestRequest (35=1)
        [ 2/9] 4.1   auto     PASS     New Order Single — Market Buy
        [ 3/9] 5.3   assisted BLOCKED  Partial fill — BLOCKED: needs counterparty action: the counterparty partially fills 40 of the 100 shares
        [ 4/9] 5.5   assisted BLOCKED  Done For Day — BLOCKED: needs counterparty action: the counterparty ends the trading day for the order (Done For Day)
        [ 5/9] 5.6   assisted BLOCKED  Expired — BLOCKED: needs counterparty action: the counterparty expires the unexecuted order
        [ 6/9] 5.8   assisted BLOCKED  AvgPx and CumQty accumulate across partial fills — BLOCKED: needs counterparty action: the counterparty fills 30 shares at 10.00; the counterparty fills 20 more shares at 11.00
        [ 7/9] 6.4   assisted BLOCKED  Cancel Reject — too late to cancel (order pending fill) — BLOCKED: needs counterparty action: the counterparty holds the order in a pending-fill state so that a cancel is too late
        [ 8/9] 8.1   assisted BLOCKED  Mid-session disconnect: ResendRequest / gap fill on reconnect — BLOCKED: needs counterparty action: the counterparty advances its outbound MsgSeqNum by 3 (messages we will not have seen)
        [ 9/9] 8.7   assisted BLOCKED  Exchange restart: reconnect, gap fill, open orders live — BLOCKED: needs counterparty action: the counterparty restarts its FIX engine (the session drops and comes back)
        
        order-entry-fix42 — Order Entry Certification — FIX 4.2 US Equities
        target generic, session emu42 (FIX.4.2), run 20260929-090536.042, agent 0.3.0 (a3)
        
        ID   TITLE                                             REQ  MODE      STATUS   REASON
        3.4  Exchange-initiated TestRequest answered           req  assisted  BLOCKED  BLOCKED: needs counterparty action: the counterparty sends a TestRequest (35=1)
        4.1  New Order Single — Market Buy                     req  auto      PASS     
        5.3  Partial fill                                      req  assisted  BLOCKED  BLOCKED: needs counterparty action: the counterparty partially fills 40 of the 100 shares
        5.5  Done For Day                                      opt  assisted  BLOCKED  BLOCKED: needs counterparty action: the counterparty ends the trading day for the order (Done For Day)
        5.6  Expired                                           opt  assisted  BLOCKED  BLOCKED: needs counterparty action: the counterparty expires the unexecuted order
        5.8  AvgPx and CumQty accumulate across partial fills  req  assisted  BLOCKED  BLOCKED: needs counterparty action: the counterparty fills 30 shares at 10.00; the counterparty fills 20 more shares at 11.00
        6.4  Cancel Reject — too late to cancel (order pendi…  req  assisted  BLOCKED  BLOCKED: needs counterparty action: the counterparty holds the order in a pending-fill state so that a cancel is too late
        8.1  Mid-session disconnect: ResendRequest / gap fil…  req  assisted  BLOCKED  BLOCKED: needs counterparty action: the counterparty advances its outbound MsgSeqNum by 3 (messages we will not have seen)
        8.7  Exchange restart: reconnect, gap fill, open ord…  opt  assisted  BLOCKED  BLOCKED: needs counterparty action: the counterparty restarts its FIX engine (the session drops and comes back)
        
        all cases     : PASS 1, BLOCKED 8
        required cases: PASS 1, BLOCKED 5
        exit code     : 7
        results       : data/certs/20260929-090536.042
--- PASS: TestCertGenericTargetBlocksAssisted (13.12s)
=== RUN   TestCertBrokenCaseAndError
    cert_test.go:285: emulator up: fix=55466 strict=55467 api=55468 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCertBrokenCaseAndError1250238561/001/emulator
    cert_test.go:288: $ orderecho cert run --suite certs/broken.yaml --target certs/targets/emulator.yaml --session emu42 --case B.1  -> exit 5
        OrderEcho 0.3.0 (a3) cert run 20260929-090537.940
          suite   : broken (FIX.4.2, 3 cases) certs/broken.yaml
          target  : orderecho-emulator certs/targets/emulator.yaml
          session : emu42 AGENT -> ORDERECHO @ 127.0.0.1:55466
        
        [ 1/1] B.1   auto     FAIL     Expects FILLED on a hold symbol — step 3 (expect): timed out after 3s waiting for ord_status=FILLED from the counterparty; last relevant message: 35=8 seq=2 11=OE-20260929-090537.940-1 37=O-202…
        
        broken — Deliberately broken cases (test only)
        target orderecho-emulator, session emu42 (FIX.4.2), run 20260929-090537.940, agent 0.3.0 (a3)
        
        ID   TITLE                            REQ  MODE  STATUS  REASON
        B.1  Expects FILLED on a hold symbol  req  auto  FAIL    step 3 (expect): timed out after 3s waiting for ord_status=FILLED from the counterparty; last relevant message: 35=8 seq=2 11=OE-20260929-090537.940-…
        
        all cases     : FAIL 1
        required cases: FAIL 1
        exit code     : 5
        results       : data/certs/20260929-090537.940
    cert_test.go:309: cert run with the emulator killed:
        OrderEcho 0.3.0 (a3) cert run 20260929-090541.248
          suite   : broken (FIX.4.2, 3 cases) certs/broken.yaml
          target  : orderecho-emulator certs/targets/emulator.yaml
          session : emu42 AGENT -> ORDERECHO @ 127.0.0.1:55466
        
        [ 1/2] B.2   auto     ERROR    A long wait the emulator dies during — step 1 (session): session dropped: DROPPED after logon: connection closed by counterparty
        [ 2/2] B.3   auto     ERROR    After the counterparty is gone — session unavailable: logon failed: DROPPED after logon: connection closed by counterparty
        
        broken — Deliberately broken cases (test only)
        target orderecho-emulator, session emu42 (FIX.4.2), run 20260929-090541.248, agent 0.3.0 (a3)
        
        ID   TITLE                                 REQ  MODE  STATUS  REASON
        B.2  A long wait the emulator dies during  req  auto  ERROR   step 1 (session): session dropped: DROPPED after logon: connection closed by counterparty
        B.3  After the counterparty is gone        req  auto  ERROR   session unavailable: logon failed: DROPPED after logon: connection closed by counterparty
        
        all cases     : ERROR 2
        required cases: ERROR 2
        session       : logon failed: DROPPED after logon: connection closed by counterparty; reconnect failed: logon failed: DROPPED after logon: connection closed by counterparty
        exit code     : 3
        results       : data/certs/20260929-090541.248
--- PASS: TestCertBrokenCaseAndError (6.33s)
=== RUN   TestCLIOrderExitCodes
    cli_test.go:40: emulator up: fix=55483 strict=55484 api=55485 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLIOrderExitCodes1115339183/001/emulator
    cli_test.go:43: $ orderecho order --session emu44 AAPL 100 buy mkt  -> exit 0
        OrderEcho agent 0.3.0 (a3) - session emu44
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:55483
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090544.272.jsonl
          fix log        : logs/fix/emu44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:44.272 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:55483 evidence=data/evidence/20260929-090544.272.jsonl
        20260929-09:05:44.273 INFO    session  emu44  Connecting to 127.0.0.1:55483
        20260929-09:05:44.274 INFO    session  emu44  Connected to 127.0.0.1:55483 (local 127.0.0.1:55494)
        20260929-09:05:44.327 INFO    session  emu44  connected: sending Logon
        20260929-09:05:44.327 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
        20260929-09:05:44.327 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-09:05:44.326|98=0|108=30|141=Y|10=072|
        20260929-09:05:44.327 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:44.331 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:05:44.331|98=0|108=30|141=Y|10=068|
        20260929-09:05:44.341 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:05:44.341 INFO    session  emu44  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-09:05:44.347 OUT  seq=2    35=D  8=FIX.4.4|9=140|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-09:05:44.347|11=OE-20260929-090544.272-1|21=1|55=AAPL|54=1|60=20260929-09:05:44.341|38=100|40=1|10=023|
        >> D 11=OE-20260929-090544.272-1 BUY 100 AAPL MKT sent
        20260929-09:05:44.350 IN   seq=2    35=8  8=FIX.4.4|9=227|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:44.350|37=O-20260929-090544-1|11=OE-20260929-090544.272-1|17=E-20260929-090544-1|150=0|39=0|55=AAPL|54=1|38=100|40=1|32=0|31=0.00|151=100|14=0|6=0.0000|60=20260929-09:05:44.348|10=022|
        20260929-09:05:44.354 INFO    session  emu44  application message received: 35=8 (ExecutionReport) seq=2
        20260929-09:05:44.354 INFO    session  emu44  << ER 11=OE-20260929-090544.272-1 37=O-20260929-090544-1 150=0(New) 39=0(New) cum=0 leaves=100 avg=0.0000  -> NEW cum=0 leaves=100  checks: PASS
        20260929-09:05:44.943 IN   seq=3    35=8  8=FIX.4.4|9=233|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:05:44.942|37=O-20260929-090544-1|11=OE-20260929-090544.272-1|17=E-20260929-090544-2|150=F|39=2|55=AAPL|54=1|38=100|40=1|32=100|31=227.50|151=0|14=100|6=227.5000|60=20260929-09:05:44.940|10=115|
        20260929-09:05:44.985 INFO    session  emu44  application message received: 35=8 (ExecutionReport) seq=3
        20260929-09:05:44.985 INFO    session  emu44  << ER 11=OE-20260929-090544.272-1 37=O-20260929-090544-1 150=F(Trade) 39=2(Filled) last=100@227.50 cum=100 leaves=0 avg=227.5000  -> FILLED cum=100 leaves=0  checks: PASS
        
        Order chain for OE-20260929-090544.272-1
          ClOrdIDs: OE-20260929-090544.272-1
          OrderID : O-20260929-090544-1
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T09:05:44.347 <--  NewOrderSingle       - / -                            100              -       -       -          -
          2026-09-29T09:05:44.354 -->  ExecutionReport      0 (New) / 0 (New)                100              -       0     100     0.0000
          2026-09-29T09:05:44.985 -->  ExecutionReport      F (Trade) / 2 (Filled)           100     100@227.50     100       0   227.5000
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 100 without ever falling
          [PASS] working_quantities: 1 working report(s) balanced
          [PASS] terminal_quantities: 1 terminal report(s) consistent
          [PASS] fill_quantities_sum: 1 fill(s) totalling 100 match CumQty
          [PASS] avg_px: AvgPx 227.5000 matches the fills to within 0.0001
          [PASS] exec_ids_unique: 2 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-090544-1 throughout
          [PASS] nothing_after_terminal: terminal 39=2 was the last word
          [PASS] version_rules: every report matches its version's conventions
          [PASS] requests_answered: all 1 request(s) answered
          [PASS] framing_intact: all 3 message(s) correctly framed
        
          verdict: PASS
        20260929-09:05:44.992 OUT  seq=3    35=5  8=FIX.4.4|9=88|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-09:05:44.992|58=OrderEcho agent: order done|10=150|
        20260929-09:05:44.992 INFO    session  emu44  logout initiated: OrderEcho agent: order done
        20260929-09:05:44.992 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
        20260929-09:05:44.995 IN   seq=4    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=4|52=20260929-09:05:44.995|58=Logout acknowledged|10=046|
        20260929-09:05:45.000 INFO    session  emu44  logout confirmed: Logout acknowledged
        20260929-09:05:45.000 INFO    session  emu44  Disconnecting: Logout confirmed
        20260929-09:05:45.006 INFO    session  emu44  disconnected: next_out=4 next_in=5
        20260929-09:05:45.006 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
        20260929-09:05:45.006 INFO    session  emu44  Connection closed, peer=127.0.0.1:55483
        20260929-09:05:45.006 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    cli_test.go:53: $ orderecho order --session emu42 EFG 1000 buy lmt 10.00 --wait 2500ms  -> exit 6
        OrderEcho agent 0.3.0 (a3) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:55483
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090545.017.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:45.018 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:55483 evidence=data/evidence/20260929-090545.017.jsonl
        20260929-09:05:45.018 INFO    session  emu42  Connecting to 127.0.0.1:55483
        20260929-09:05:45.018 INFO    session  emu42  Connected to 127.0.0.1:55483 (local 127.0.0.1:55495)
        20260929-09:05:45.027 INFO    session  emu42  connected: sending Logon
        20260929-09:05:45.027 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-09:05:45.027 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-09:05:45.026|98=0|108=30|141=Y|10=068|
        20260929-09:05:45.027 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:45.030 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:05:45.030|98=0|108=30|141=Y|10=063|
        20260929-09:05:45.035 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:05:45.035 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-09:05:45.041 OUT  seq=2    35=D  8=FIX.4.2|9=149|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-09:05:45.041|11=OE-20260929-090545.017-1|21=1|55=EFG|54=1|60=20260929-09:05:45.035|38=1000|40=2|44=10.00|10=143|
        >> D 11=OE-20260929-090545.017-1 BUY 1000 EFG LMT 10.00 sent
        20260929-09:05:45.046 IN   seq=2    35=8  8=FIX.4.2|9=242|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:45.045|37=O-20260929-090544-2|11=OE-20260929-090545.017-1|17=E-20260929-090544-3|20=0|150=0|39=0|55=EFG|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-09:05:45.043|10=135|
        20260929-09:05:45.050 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=2
        20260929-09:05:45.051 INFO    session  emu42  << ER 11=OE-20260929-090545.017-1 37=O-20260929-090544-2 150=0(New) 39=0(New) cum=0 leaves=1000 avg=0.0000  -> NEW cum=0 leaves=1000  checks: PASS
        20260929-09:05:45.642 IN   seq=3    35=8  8=FIX.4.2|9=247|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:05:45.642|37=O-20260929-090544-2|11=OE-20260929-090545.017-1|17=E-20260929-090544-4|20=0|150=1|39=1|55=EFG|54=1|38=1000|40=2|44=10.00|32=400|31=10.00|151=600|14=400|6=10.0000|60=20260929-09:05:45.640|10=149|
        20260929-09:05:45.694 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=3
        20260929-09:05:45.694 INFO    session  emu42  << ER 11=OE-20260929-090545.017-1 37=O-20260929-090544-2 150=1(Partial fill) 39=1(Partially filled) last=400@10.00 cum=400 leaves=600 avg=10.0000  -> PARTIALLY_FILLED cum=400 leaves=600  checks: PASS
        20260929-09:05:46.053 IN   seq=4    35=8  8=FIX.4.2|9=247|35=8|49=ORDERECHO|56=AGENT|34=4|52=20260929-09:05:46.052|37=O-20260929-090544-2|11=OE-20260929-090545.017-1|17=E-20260929-090544-5|20=0|150=1|39=1|55=EFG|54=1|38=1000|40=2|44=10.00|32=100|31=10.00|151=500|14=500|6=10.0000|60=20260929-09:05:46.050|10=140|
        20260929-09:05:46.058 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=4
        20260929-09:05:46.058 INFO    session  emu42  << ER 11=OE-20260929-090545.017-1 37=O-20260929-090544-2 150=1(Partial fill) 39=1(Partially filled) last=100@10.00 cum=500 leaves=500 avg=10.0000  -> PARTIALLY_FILLED cum=500 leaves=500  checks: PASS
        >>> order OE-20260929-090545.017-1 still PARTIALLY_FILLED after 2.5s
        
        Order chain for OE-20260929-090545.017-1
          ClOrdIDs: OE-20260929-090545.017-1
          OrderID : O-20260929-090544-2
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T09:05:45.041 <--  NewOrderSingle       - / -                           1000              -       -       -          -
          2026-09-29T09:05:45.050 -->  ExecutionReport      0 (New) / 0 (New)               1000              -       0    1000     0.0000
          2026-09-29T09:05:45.693 -->  ExecutionReport      1 (Partial fill) / 1 (Partially filled)    1000      400@10.00     400     600    10.0000
          2026-09-29T09:05:46.058 -->  ExecutionReport      1 (Partial fill) / 1 (Partially filled)    1000      100@10.00     500     500    10.0000
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 500 without ever falling
          [PASS] working_quantities: 3 working report(s) balanced
          [PASS] terminal_quantities: no terminal reports to check
          [PASS] fill_quantities_sum: 2 fill(s) totalling 500 match CumQty
          [PASS] avg_px: AvgPx 10.0000 matches the fills to within 0.0001
          [PASS] exec_ids_unique: 3 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-090544-2 throughout
          [PASS] nothing_after_terminal: the order never reached a terminal state
          [PASS] version_rules: every report matches its version's conventions
          [PASS] requests_answered: all 1 request(s) answered
          [PASS] framing_intact: all 4 message(s) correctly framed
        
          verdict: PASS
        20260929-09:05:47.564 OUT  seq=3    35=5  8=FIX.4.2|9=88|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-09:05:47.564|58=OrderEcho agent: order done|10=146|
        20260929-09:05:47.564 INFO    session  emu42  logout initiated: OrderEcho agent: order done
        20260929-09:05:47.564 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-09:05:47.567 IN   seq=5    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=5|52=20260929-09:05:47.566|58=Logout acknowledged|10=042|
        20260929-09:05:47.571 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-09:05:47.571 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-09:05:47.577 INFO    session  emu42  disconnected: next_out=4 next_in=6
        20260929-09:05:47.577 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-09:05:47.577 INFO    session  emu42  Connection closed, peer=127.0.0.1:55483
        20260929-09:05:47.577 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    cli_test.go:80: POST /sessions/agent42/inject/next -> {"queued":{"id":1,"msg_type":"8","set":{"14":"999"},"remove":[],"corrupt_checksum":false,"count":1,"remaining":1}}
    cli_test.go:81: POST /orders/O-20260929-090544-3/fill-rest -> {"session":"agent42","order":{"order_id":"O-20260929-090544-3","cl_ord_id":"OE-20260929-090547.589-1","symbol":"ZWZZT","side":"1","order_qty":"1000","cum_qty":"1000","leaves_qty":"0","avg_px":"10.0000","ord_status":"2","price_source":"limit","rule_name":"nasdaq-test-hold"},"sent":[{"seq":3,"msg_type":"8","raw":"8=FIX.4.2|9=248|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:05:47.697|37=O-20260929-090544-3|11=OE-20260929-090547.589-1|17=E-20260929-090544-7|20=0|150=2|39=2|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=1000|31=10.00|151=0|14=999|6=10.0000|60=20260929-09:05:47.695|10=137|","injected":true}]}
    cli_test.go:62: $ orderecho order --session emu42 ZWZZT 1000 buy lmt 10.00 --wait 20s  -> exit 5
        OrderEcho agent 0.3.0 (a3) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:55483
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=4 next_in=6)
          evidence file  : data/evidence/20260929-090547.589.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:47.589 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:55483 evidence=data/evidence/20260929-090547.589.jsonl
        20260929-09:05:47.590 INFO    session  emu42  Connecting to 127.0.0.1:55483
        20260929-09:05:47.590 INFO    session  emu42  Connected to 127.0.0.1:55483 (local 127.0.0.1:55497)
        20260929-09:05:47.641 INFO    session  emu42  connected: sending Logon
        20260929-09:05:47.641 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-09:05:47.641 INFO    session  emu42  store archived: outbound message store archived to data/msgstore/emu42.jsonl.20260929-090547 (Logon will carry 141=Y)
        20260929-09:05:47.641 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-09:05:47.641|98=0|108=30|141=Y|10=073|
        20260929-09:05:47.641 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:47.645 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:05:47.645|98=0|108=30|141=Y|10=077|
        20260929-09:05:47.649 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:05:47.649 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-09:05:47.655 OUT  seq=2    35=D  8=FIX.4.2|9=151|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-09:05:47.655|11=OE-20260929-090547.589-1|21=1|55=ZWZZT|54=1|60=20260929-09:05:47.649|38=1000|40=2|44=10.00|10=153|
        >> D 11=OE-20260929-090547.589-1 BUY 1000 ZWZZT LMT 10.00 sent
        20260929-09:05:47.658 IN   seq=2    35=8  8=FIX.4.2|9=244|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:47.658|37=O-20260929-090544-3|11=OE-20260929-090547.589-1|17=E-20260929-090544-6|20=0|150=0|39=0|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-09:05:47.656|10=156|
        20260929-09:05:47.667 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=2
        20260929-09:05:47.667 INFO    session  emu42  << ER 11=OE-20260929-090547.589-1 37=O-20260929-090544-3 150=0(New) 39=0(New) cum=0 leaves=1000 avg=0.0000  -> NEW cum=0 leaves=1000  checks: PASS
        20260929-09:05:47.698 IN   seq=3    35=8  8=FIX.4.2|9=248|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:05:47.697|37=O-20260929-090544-3|11=OE-20260929-090547.589-1|17=E-20260929-090544-7|20=0|150=2|39=2|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=1000|31=10.00|151=0|14=999|6=10.0000|60=20260929-09:05:47.695|10=137|
        20260929-09:05:47.702 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=3
        20260929-09:05:47.702 INFO    session  emu42  << ER 11=OE-20260929-090547.589-1 37=O-20260929-090544-3 150=2(Fill) 39=2(Filled) last=1000@10.00 cum=999 leaves=0 avg=10.0000  -> FILLED cum=1000 leaves=0  checks: FAIL
        20260929-09:05:47.702 ERROR   session  emu42  check terminal_quantities: FAIL terminal_quantities: 39=2 (Filled) with CumQty 999 but OrderQty 1000, at seq=3 17=E-20260929-090544-7 (order OE-20260929-090547.589-1)
        20260929-09:05:47.702 ERROR   session  emu42  check fill_quantities_sum: FAIL fill_quantities_sum: 1 fill(s) totalling 1000 but the last CumQty is 999 (order OE-20260929-090547.589-1)
        
        Order chain for OE-20260929-090547.589-1
          ClOrdIDs: OE-20260929-090547.589-1
          OrderID : O-20260929-090544-3
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T09:05:47.655 <--  NewOrderSingle       - / -                           1000              -       -       -          -
          2026-09-29T09:05:47.667 -->  ExecutionReport      0 (New) / 0 (New)               1000              -       0    1000     0.0000
          2026-09-29T09:05:47.702 -->  ExecutionReport      2 (Fill) / 2 (Filled)           1000     1000@10.00     999       0    10.0000
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 999 without ever falling
          [PASS] working_quantities: 1 working report(s) balanced
          [FAIL] terminal_quantities: 39=2 (Filled) with CumQty 999 but OrderQty 1000, at seq=3 17=E-20260929-090544-7
          [FAIL] fill_quantities_sum: 1 fill(s) totalling 1000 but the last CumQty is 999
          [PASS] avg_px: AvgPx 10.0000 matches the fills to within 0.0001
          [PASS] exec_ids_unique: 2 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-090544-3 throughout
          [PASS] nothing_after_terminal: terminal 39=2 was the last word
          [PASS] version_rules: every report matches its version's conventions
          [PASS] requests_answered: all 1 request(s) answered
          [PASS] framing_intact: all 3 message(s) correctly framed
        
          verdict: FAIL
        20260929-09:05:47.711 OUT  seq=3    35=5  8=FIX.4.2|9=88|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-09:05:47.711|58=OrderEcho agent: order done|10=140|
        20260929-09:05:47.711 INFO    session  emu42  logout initiated: OrderEcho agent: order done
        20260929-09:05:47.711 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-09:05:47.714 IN   seq=4    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=4|52=20260929-09:05:47.713|58=Logout acknowledged|10=035|
        20260929-09:05:47.719 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-09:05:47.719 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-09:05:47.725 INFO    session  emu42  disconnected: next_out=4 next_in=5
        20260929-09:05:47.725 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-09:05:47.725 INFO    session  emu42  Connection closed, peer=127.0.0.1:55483
        20260929-09:05:47.725 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    cli_test.go:87: $ orderecho order --session strict --fix-version FIX.4.4 AAPL 100 buy mkt  -> exit 1
        OrderEcho agent 0.3.0 (a3) - session strict
          config         : orderecho.yaml
          route          : AGENT -> STRICTBRK  FIX.4.4  127.0.0.1:55484
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/strict.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090547.737.jsonl
          fix log        : logs/fix/strict_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:47.737 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=strict FIX.4.4 AGENT->STRICTBRK@127.0.0.1:55484 evidence=data/evidence/20260929-090547.737.jsonl
        20260929-09:05:47.877 INFO    session  strict  Connecting to 127.0.0.1:55484
        20260929-09:05:47.878 INFO    session  strict  Connected to 127.0.0.1:55484 (local 127.0.0.1:55502)
        20260929-09:05:47.915 INFO    session  strict  connected: sending Logon
        20260929-09:05:47.915 INFO    session  strict  seqnums reset: Logon will carry 141=Y
        20260929-09:05:47.915 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-09:05:47.915|98=0|108=30|141=Y|10=108|
        20260929-09:05:47.915 INFO    session  strict  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:47.917 IN   seq=1    35=5  8=FIX.4.2|9=100|35=5|49=STRICTBRK|56=AGENT|34=1|52=20260929-09:05:47.917|58=Incorrect BeginString, expected FIX.4.2|10=118|
        20260929-09:05:47.922 WARNING session  strict  logon refused: counterparty answered Logon with Logout: Incorrect BeginString, expected FIX.4.2
        20260929-09:05:47.922 OUT  seq=2    35=5  8=FIX.4.4|9=80|35=5|49=AGENT|56=STRICTBRK|34=2|52=20260929-09:05:47.922|58=Logout acknowledged|10=066|
        20260929-09:05:47.922 INFO    session  strict  Disconnecting: Logon refused by counterparty: Incorrect BeginString, expected FIX.4.2
        20260929-09:05:47.926 INFO    session  strict  disconnected: next_out=3 next_in=1
        20260929-09:05:47.926 INFO    session  strict  State LOGON_SENT -> DISCONNECTED
        20260929-09:05:47.926 INFO    session  strict  Connection closed, peer=127.0.0.1:55484
        20260929-09:05:47.926 INFO    session  engine  Shutdown complete
        >>> LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
        orderecho: LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
    cli_test.go:97: $ orderecho order --session emu42 AAPL 0 buy mkt  -> exit 2
        orderecho: quantity must be a positive whole number, got "0"
    cli_test.go:101: $ orderecho --help  -> exit 0
        usage: orderecho [--config PATH] <command> [flags]
        
        commands:
          version                      print version and build
          sessions                     list configured sessions
          status --session ID          persisted seqnums and outbound store size (offline)
          connect --session ID         log on and stay connected
                  [--duration 30s]     log out cleanly after this long (default: until Ctrl+C)
                  [--test-request]     send one TestRequest after logon
          order --session ID SYM QTY buy|sell|short mkt|lmt [PX]
                  [--tif day|gtc|opg|ioc|fok] [--wait 15s]
                                       send one order, print each report with its live
                                       checks, wait for a terminal state, print the
                                       timeline and verdict, log out
          session --session ID         interactive (and pipe-friendly) order session:
                                         order SYM QTY buy|sell|short mkt|lmt [PX] [TIF]
                                         cancel <ClOrdID|last>
                                         replace <ClOrdID|last> QTY [PX]
                                         status | timeline <ClOrdID|last> | resend B [E]
                                         testreq | help | quit
          cert list | show --suite F | run --suite F --target F --session ID [...]
                                       certification runner (see "orderecho cert help")
          timeline FILE... (--clordid X | --order-id X) [--json]
                                       offline checks on any log (OrderEcho FIX log,
                                       evidence JSONL, or raw FIX lines)
        
        flags for every command that connects (connect, order, session):
          --session ID                 the configured session
          --fix-version FIX.4.2|FIX.4.4  override the session's fix_version
          --reset                      reset sequence numbers (Logon carries 141=Y)
          --reconnect                  reconnect after a dropped connection
        
        exit codes (commands that connect):
          0  clean logout (order: checks PASS or WARN)
          1  logon refused or failed
          2  config or usage error
          3  connection dropped after logon
          4  logout timeout (our Logout was never answered)
          5  order checks FAIL
          6  timed out waiting for an order to reach a terminal state
          130 second Ctrl+C (exit without waiting)
        exit codes (timeline): 0 PASS, 1 WARN, 2 FAIL or nothing found, as the Python viewer.
        exit codes (cert run): 0 all required PASS/N/A, 5 required FAIL, 7 required BLOCKED/PENDING,
          8 runner ERROR, 1/3/4 session failure, 2 usage.
--- PASS: TestCLIOrderExitCodes (4.65s)
=== RUN   TestCLISessionPipedAndTimeline
    cli_test.go:109: emulator up: fix=55503 strict=55504 api=55505 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLISessionPipedAndTimeline311451127/001/emulator
    cli_test.go:112: $ orderecho session --session emu42  -> exit 0
        OrderEcho agent 0.3.0 (a3) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:55503
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090549.026.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:49.026 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:55503 evidence=data/evidence/20260929-090549.026.jsonl
        20260929-09:05:49.027 INFO    session  emu42  Connecting to 127.0.0.1:55503
        20260929-09:05:49.027 INFO    session  emu42  Connected to 127.0.0.1:55503 (local 127.0.0.1:55515)
        20260929-09:05:49.100 INFO    session  emu42  connected: sending Logon
        20260929-09:05:49.100 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-09:05:49.100 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-09:05:49.099|98=0|108=30|141=Y|10=082|
        20260929-09:05:49.100 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:49.104 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:05:49.104|98=0|108=30|141=Y|10=069|
        20260929-09:05:49.109 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:05:49.109 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        >>> session ready; type "help" for commands
        > order EFG 1000 buy lmt 10.00
        20260929-09:05:49.114 OUT  seq=2    35=D  8=FIX.4.2|9=149|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-09:05:49.114|11=OE-20260929-090549.026-1|21=1|55=EFG|54=1|60=20260929-09:05:49.109|38=1000|40=2|44=10.00|10=158|
        >> D 11=OE-20260929-090549.026-1 BUY 1000 EFG LMT 10.00 sent
        20260929-09:05:49.118 IN   seq=2    35=8  8=FIX.4.2|9=242|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:49.117|37=O-20260929-090548-1|11=OE-20260929-090549.026-1|17=E-20260929-090548-1|20=0|150=0|39=0|55=EFG|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-09:05:49.116|10=153|
        20260929-09:05:49.128 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=2
        20260929-09:05:49.128 INFO    session  emu42  << ER 11=OE-20260929-090549.026-1 37=O-20260929-090548-1 150=0(New) 39=0(New) cum=0 leaves=1000 avg=0.0000  -> NEW cum=0 leaves=1000  checks: PASS
        > order ZWZZT 500 buy lmt 10.00
        20260929-09:05:49.186 OUT  seq=3    35=D  8=FIX.4.2|9=150|35=D|49=AGENT|56=ORDERECHO|34=3|52=20260929-09:05:49.186|11=OE-20260929-090549.026-2|21=1|55=ZWZZT|54=1|60=20260929-09:05:49.180|38=500|40=2|44=10.00|10=091|
        >> D 11=OE-20260929-090549.026-2 BUY 500 ZWZZT LMT 10.00 sent
        20260929-09:05:49.190 IN   seq=3    35=8  8=FIX.4.2|9=242|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:05:49.189|37=O-20260929-090548-2|11=OE-20260929-090549.026-2|17=E-20260929-090548-2|20=0|150=0|39=0|55=ZWZZT|54=1|38=500|40=2|44=10.00|32=0|31=0.00|151=500|14=0|6=0.0000|60=20260929-09:05:49.188|10=062|
        20260929-09:05:49.194 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=3
        20260929-09:05:49.194 INFO    session  emu42  << ER 11=OE-20260929-090549.026-2 37=O-20260929-090548-2 150=0(New) 39=0(New) cum=0 leaves=500 avg=0.0000  -> NEW cum=0 leaves=500  checks: PASS
        > replace last 800 10.50
        20260929-09:05:49.251 OUT  seq=4    35=G  8=FIX.4.2|9=201|35=G|49=AGENT|56=ORDERECHO|34=4|52=20260929-09:05:49.251|41=OE-20260929-090549.026-2|11=OE-20260929-090549.026-3|37=O-20260929-090548-2|21=1|55=ZWZZT|54=1|60=20260929-09:05:49.246|38=800|40=2|44=10.50|10=120|
        >> G 11=OE-20260929-090549.026-3 41=OE-20260929-090549.026-2 qty=800 10.50 sent
        20260929-09:05:49.254 IN   seq=4    35=8  8=FIX.4.2|9=270|35=8|49=ORDERECHO|56=AGENT|34=4|52=20260929-09:05:49.254|37=O-20260929-090548-2|11=OE-20260929-090549.026-3|41=OE-20260929-090549.026-2|17=E-20260929-090548-3|20=0|150=5|39=0|55=ZWZZT|54=1|38=800|40=2|44=10.50|32=0|31=0.00|151=800|14=0|6=0.0000|60=20260929-09:05:49.252|10=210|
        20260929-09:05:49.259 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=4
        20260929-09:05:49.259 INFO    session  emu42  << ER 11=OE-20260929-090549.026-3 37=O-20260929-090548-2 150=5(Replaced) 39=0(New) cum=0 leaves=800 avg=0.0000  -> NEW cum=0 leaves=800  checks: PASS
        > cancel last
        20260929-09:05:49.355 OUT  seq=5    35=F  8=FIX.4.2|9=182|35=F|49=AGENT|56=ORDERECHO|34=5|52=20260929-09:05:49.355|41=OE-20260929-090549.026-3|11=OE-20260929-090549.026-4|37=O-20260929-090548-2|55=ZWZZT|54=1|60=20260929-09:05:49.311|38=800|10=064|
        >> F 11=OE-20260929-090549.026-4 41=OE-20260929-090549.026-3 sent
        20260929-09:05:49.358 IN   seq=5    35=8  8=FIX.4.2|9=268|35=8|49=ORDERECHO|56=AGENT|34=5|52=20260929-09:05:49.358|37=O-20260929-090548-2|11=OE-20260929-090549.026-4|41=OE-20260929-090549.026-3|17=E-20260929-090548-4|20=0|150=4|39=4|55=ZWZZT|54=1|38=800|40=2|44=10.50|32=0|31=0.00|151=0|14=0|6=0.0000|60=20260929-09:05:49.357|10=131|
        20260929-09:05:49.363 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=5
        20260929-09:05:49.363 INFO    session  emu42  << ER 11=OE-20260929-090549.026-4 37=O-20260929-090548-2 150=4(Canceled) 39=4(Canceled) cum=0 leaves=0 avg=0.0000  -> CANCELED cum=0 leaves=0  checks: PASS
        > status
        CLORDID                      ORDERID                SYMBOL SIDE  TYPE      QTY      CUM   LEAVES        AVG STATE / CHECKS
        OE-20260929-090549.026-1     O-20260929-090548-1    EFG    BUY   LMT      1000        0     1000     0.0000 NEW / PASS
        OE-20260929-090549.026-4     O-20260929-090548-2    ZWZZT  BUY   LMT       800        0        0     0.0000 CANCELED / PASS
        > timeline last
        Order chain for OE-20260929-090549.026-2
          ClOrdIDs: OE-20260929-090549.026-2, OE-20260929-090549.026-3, OE-20260929-090549.026-4
          OrderID : O-20260929-090548-2
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T09:05:49.186 <--  NewOrderSingle       - / -                            500              -       -       -          -
          2026-09-29T09:05:49.194 -->  ExecutionReport      0 (New) / 0 (New)                500              -       0     500     0.0000
          2026-09-29T09:05:49.251 <--  OrderCancelReplaceRequest - / -                            800              -       -       -          -
          2026-09-29T09:05:49.259 -->  ExecutionReport      5 (Replaced) / 0 (New)           800              -       0     800     0.0000
          2026-09-29T09:05:49.355 <--  OrderCancelRequest   - / -                            800              -       -       -          -
          2026-09-29T09:05:49.363 -->  ExecutionReport      4 (Canceled) / 4 (Canceled)      800              -       0       0     0.0000
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 0 without ever falling
          [PASS] working_quantities: 2 working report(s) balanced
          [PASS] terminal_quantities: 1 terminal report(s) consistent
          [PASS] fill_quantities_sum: no fills in this chain
          [PASS] avg_px: no fills to average
          [PASS] exec_ids_unique: 3 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-090548-2 throughout
          [PASS] nothing_after_terminal: terminal 39=4 was the last word
          [PASS] version_rules: every report matches its version's conventions
          [PASS] requests_answered: all 3 request(s) answered
          [PASS] framing_intact: all 6 message(s) correctly framed
        
          verdict: PASS
        > resend 1 2
        20260929-09:05:49.422 INFO    session  emu42  resend request sent: 7=1 16=2 (on request)
        20260929-09:05:49.422 OUT  seq=6    35=2  8=FIX.4.2|9=66|35=2|49=AGENT|56=ORDERECHO|34=6|52=20260929-09:05:49.422|7=1|16=2|10=117|
        20260929-09:05:49.422 INFO    session  emu42  ResendRequest sent: 7=1 16=2
        >> ResendRequest 7=1 16=2 sent
        > testreq
        20260929-09:05:49.427 OUT  seq=7    35=1  8=FIX.4.2|9=68|35=1|49=AGENT|56=ORDERECHO|34=7|52=20260929-09:05:49.427|112=TEST-1|10=111|
        >> TestRequest TEST-1 sent
        20260929-09:05:49.428 IN   seq=1    35=4  8=FIX.4.2|9=99|35=4|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:05:49.428|43=Y|122=20260929-09:05:49.427|123=Y|36=2|10=029|
        20260929-09:05:49.428 WARNING session  emu42  possdup ignored: MsgSeqNum 1 below expected 6, 43=Y
        20260929-09:05:49.429 IN   seq=2    35=8  8=FIX.4.2|9=273|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:49.428|43=Y|122=20260929-09:05:49.117|37=O-20260929-090548-1|11=OE-20260929-090549.026-1|17=E-20260929-090548-1|20=0|150=0|39=0|55=EFG|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-09:05:49.116|10=180|
        20260929-09:05:49.429 WARNING session  emu42  possdup ignored: MsgSeqNum 2 below expected 6, 43=Y
        20260929-09:05:49.431 IN   seq=6    35=0  8=FIX.4.2|9=68|35=0|49=ORDERECHO|56=AGENT|34=6|52=20260929-09:05:49.431|112=TEST-1|10=104|
        20260929-09:05:49.436 INFO    session  emu42  testrequest answered: 112=TEST-1
        >> TestRequest TEST-1 answered
        > quit
        20260929-09:05:49.441 OUT  seq=8    35=5  8=FIX.4.2|9=90|35=5|49=AGENT|56=ORDERECHO|34=8|52=20260929-09:05:49.441|58=OrderEcho agent: session done|10=116|
        20260929-09:05:49.441 INFO    session  emu42  logout initiated: OrderEcho agent: session done
        20260929-09:05:49.441 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-09:05:49.443 IN   seq=7    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=7|52=20260929-09:05:49.442|58=Logout acknowledged|10=039|
        20260929-09:05:49.447 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-09:05:49.447 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-09:05:49.451 INFO    session  emu42  disconnected: next_out=9 next_in=8
        20260929-09:05:49.451 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-09:05:49.451 INFO    session  emu42  Connection closed, peer=127.0.0.1:55503
        20260929-09:05:49.451 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    cli_test.go:127: $ orderecho timeline /var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLISessionPipedAndTimeline311451127/002/cli/logs/fix/emu42_20260929.log --clordid OE-20260929-090549.026-3 --json  -> exit 0
        {
          "seed": "OE-20260929-090549.026-3",
          "cl_ord_ids": [
            "OE-20260929-090549.026-2",
            "OE-20260929-090549.026-3",
            "OE-20260929-090549.026-4"
          ],
          "order_ids": [
            "O-20260929-090548-2"
          ],
          "steps": 6,
          "checks": [
            {
              "name": "cum_qty_monotonic",
              "status": "PASS",
              "explanation": "CumQty rose to 0 without ever falling",
              "rule": "CumQty never decreases"
            },
            {
              "name": "working_quantities",
              "status": "PASS",
              "explanation": "2 working report(s) balanced",
              "rule": "working states: CumQty + LeavesQty == OrderQty"
            },
            {
              "name": "terminal_quantities",
              "status": "PASS",
              "explanation": "1 terminal report(s) consistent",
              "rule": "terminal states: LeavesQty == 0, and 39=2 implies CumQty == OrderQty"
            },
            {
              "name": "fill_quantities_sum",
              "status": "PASS",
              "explanation": "no fills in this chain",
              "rule": "sum of LastQty over fills == final CumQty"
            },
            {
              "name": "avg_px",
              "status": "PASS",
              "explanation": "no fills to average",
              "rule": "AvgPx == sum(LastQty x LastPx) / CumQty, within 0.0001"
            },
            {
              "name": "exec_ids_unique",
              "status": "PASS",
              "explanation": "3 ExecID(s), all distinct",
              "rule": "ExecIDs are unique within the chain"
            },
            {
              "name": "order_id_constant",
              "status": "PASS",
              "explanation": "OrderID O-20260929-090548-2 throughout",
              "rule": "OrderID is constant across the chain"
            },
            {
              "name": "nothing_after_terminal",
              "status": "PASS",
              "explanation": "terminal 39=4 was the last word",
              "rule": "no state change after a terminal report (replays excepted)"
            },
            {
              "name": "version_rules",
              "status": "PASS",
              "explanation": "every report matches its version's conventions",
              "rule": "ExecType and ExecTransType match the message's FIX version"
            },
            {
              "name": "requests_answered",
              "status": "PASS",
              "explanation": "all 3 request(s) answered",
              "rule": "each D/F/G is answered by an ER, a cancel reject or a Reject"
            },
            {
              "name": "framing_intact",
              "status": "PASS",
              "explanation": "all 6 message(s) correctly framed",
              "rule": "every message in the chain is correctly framed (9 and 10 agree)"
            }
          ],
          "verdict": "PASS"
        }
    cli_test.go:142: $ orderecho timeline /var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLISessionPipedAndTimeline311451127/003/emu42_20260929.log --clordid OE-20260929-090549.026-3  -> exit 1
        Order chain for OE-20260929-090549.026-3
          ClOrdIDs: OE-20260929-090549.026-2, OE-20260929-090549.026-3, OE-20260929-090549.026-4
          OrderID : O-20260929-090548-2
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T09:05:49.186 <--  NewOrderSingle       - / -                            500              -       -       -          -
          2026-09-29T09:05:49.190 -->  ExecutionReport      0 (New) / 0 (New)                500              -       0     500     0.0000
          2026-09-29T09:05:49.251 <--  OrderCancelReplaceRequest - / -                            800              -       -       -          -
          2026-09-29T09:05:49.254 -->  ExecutionReport      5 (Replaced) / 0 (New)           800              -       0     800     0.0000
          2026-09-29T09:05:49.355 <--  OrderCancelRequest   - / -                            800              -       -       -          -
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 0 without ever falling
          [PASS] working_quantities: 2 working report(s) balanced
          [PASS] terminal_quantities: no terminal reports to check
          [PASS] fill_quantities_sum: no fills in this chain
          [PASS] avg_px: no fills to average
          [PASS] exec_ids_unique: 2 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-090548-2 throughout
          [PASS] nothing_after_terminal: the order never reached a terminal state
          [PASS] version_rules: every report matches its version's conventions
          [WARN] requests_answered: 1 request(s) with no response in this log: 35=F seq=5 11=OE-20260929-090549.026-4
          [PASS] framing_intact: all 5 message(s) correctly framed
        
          verdict: WARN
    cli_test.go:145: $ orderecho timeline /var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLISessionPipedAndTimeline311451127/002/cli/logs/fix/emu42_20260929.log --clordid NOPE  -> exit 2
        no messages found for NOPE
    cli_test.go:158: parity CLI session / agent FIX log / OE-20260929-090549.026-1: PASS (all PASS)
    cli_test.go:158: parity CLI session / emulator FIX log / OE-20260929-090549.026-1: PASS (all PASS)
    cli_test.go:158: parity CLI session / agent FIX log / OE-20260929-090549.026-2: PASS (all PASS)
    cli_test.go:158: parity CLI session / emulator FIX log / OE-20260929-090549.026-2: PASS (all PASS)
--- PASS: TestCLISessionPipedAndTimeline (1.65s)
=== RUN   TestScenario1LogonTestRequestLogout
    interop_test.go:26: emulator up: fix=55516 strict=55517 api=55518 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario1LogonTestRequestLogout852645524/001/emulator
    interop_test.go:34: emu42: TestRequest TEST-1 answered
    interop_test.go:35: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:51: emu44: TestRequest TEST-1 answered
    interop_test.go:52: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:53: POST /sessions/agent44/logout -> {"state":"LOGOUT_SENT","sent":[{"seq":3,"msg_type":"5","raw":"8=FIX.4.4|9=85|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:05:51.089|58=interop: emulator logout|10=038|","injected":false}]}
    interop_test.go:75: $ orderecho connect --session emu42 --test-request --duration 1s  -> exit 0
        OrderEcho agent 0.3.0 (a3) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:55516
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090551.121.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:51.122 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:55516 evidence=data/evidence/20260929-090551.121.jsonl
        20260929-09:05:51.122 INFO    session  emu42  Connecting to 127.0.0.1:55516
        20260929-09:05:51.123 INFO    session  emu42  Connected to 127.0.0.1:55516 (local 127.0.0.1:55534)
        20260929-09:05:51.131 INFO    session  emu42  connected: sending Logon
        20260929-09:05:51.131 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-09:05:51.131 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-09:05:51.131|98=0|108=30|141=Y|10=062|
        20260929-09:05:51.131 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:51.135 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:05:51.134|98=0|108=30|141=Y|10=065|
        20260929-09:05:51.139 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:05:51.140 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-09:05:51.145 OUT  seq=2    35=1  8=FIX.4.2|9=68|35=1|49=AGENT|56=ORDERECHO|34=2|52=20260929-09:05:51.144|112=TEST-1|10=095|
        >>> TestRequest TEST-1 sent
        20260929-09:05:51.147 IN   seq=2    35=0  8=FIX.4.2|9=68|35=0|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:51.147|112=TEST-1|10=097|
        20260929-09:05:51.152 INFO    session  emu42  testrequest answered: 112=TEST-1
        >>> TestRequest TEST-1 answered in 7ms
        >>> Duration 1s elapsed; logging out
        20260929-09:05:52.204 OUT  seq=3    35=5  8=FIX.4.2|9=94|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-09:05:52.204|58=OrderEcho agent: duration elapsed|10=004|
        20260929-09:05:52.204 INFO    session  emu42  logout initiated: OrderEcho agent: duration elapsed
        20260929-09:05:52.204 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-09:05:52.206 IN   seq=3    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:05:52.206|58=Logout acknowledged|10=027|
        20260929-09:05:52.212 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-09:05:52.212 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-09:05:52.217 INFO    session  emu42  disconnected: next_out=4 next_in=4
        20260929-09:05:52.217 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-09:05:52.217 INFO    session  emu42  Connection closed, peer=127.0.0.1:55516
        20260929-09:05:52.217 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    interop_test.go:75: $ orderecho connect --session emu44 --test-request --duration 1s  -> exit 0
        OrderEcho agent 0.3.0 (a3) - session emu44
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:55516
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090552.231.jsonl
          fix log        : logs/fix/emu44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:52.232 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:55516 evidence=data/evidence/20260929-090552.231.jsonl
        20260929-09:05:52.232 INFO    session  emu44  Connecting to 127.0.0.1:55516
        20260929-09:05:52.232 INFO    session  emu44  Connected to 127.0.0.1:55516 (local 127.0.0.1:55535)
        20260929-09:05:52.242 INFO    session  emu44  connected: sending Logon
        20260929-09:05:52.242 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
        20260929-09:05:52.242 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-09:05:52.241|98=0|108=30|141=Y|10=067|
        20260929-09:05:52.242 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:52.246 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:05:52.246|98=0|108=30|141=Y|10=072|
        20260929-09:05:52.250 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:05:52.250 INFO    session  emu44  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-09:05:52.255 OUT  seq=2    35=1  8=FIX.4.4|9=68|35=1|49=AGENT|56=ORDERECHO|34=2|52=20260929-09:05:52.255|112=TEST-1|10=101|
        >>> TestRequest TEST-1 sent
        20260929-09:05:52.257 IN   seq=2    35=0  8=FIX.4.4|9=68|35=0|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:52.257|112=TEST-1|10=102|
        20260929-09:05:52.263 INFO    session  emu44  testrequest answered: 112=TEST-1
        >>> TestRequest TEST-1 answered in 8ms
        >>> Duration 1s elapsed; logging out
        20260929-09:05:53.312 OUT  seq=3    35=5  8=FIX.4.4|9=94|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-09:05:53.312|58=OrderEcho agent: duration elapsed|10=007|
        20260929-09:05:53.312 INFO    session  emu44  logout initiated: OrderEcho agent: duration elapsed
        20260929-09:05:53.312 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
        20260929-09:05:53.315 IN   seq=3    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:05:53.314|58=Logout acknowledged|10=030|
        20260929-09:05:53.319 INFO    session  emu44  logout confirmed: Logout acknowledged
        20260929-09:05:53.319 INFO    session  emu44  Disconnecting: Logout confirmed
        20260929-09:05:53.324 INFO    session  emu44  disconnected: next_out=4 next_in=4
        20260929-09:05:53.324 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
        20260929-09:05:53.324 INFO    session  emu44  Connection closed, peer=127.0.0.1:55516
        20260929-09:05:53.324 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    interop_test.go:89: POST /sessions/agent42/logout -> {"state":"LOGOUT_SENT","sent":[{"seq":2,"msg_type":"5","raw":"8=FIX.4.2|9=78|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:53.490|58=bye from emulator|10=066|","injected":false}]}
    interop_test.go:87: $ orderecho connect --session emu42  -> exit 0
        OrderEcho agent 0.3.0 (a3) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:55516
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=4 next_in=4)
          evidence file  : data/evidence/20260929-090553.335.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:53.335 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:55516 evidence=data/evidence/20260929-090553.335.jsonl
        20260929-09:05:53.420 INFO    session  emu42  Connecting to 127.0.0.1:55516
        20260929-09:05:53.421 INFO    session  emu42  Connected to 127.0.0.1:55516 (local 127.0.0.1:55538)
        20260929-09:05:53.478 INFO    session  emu42  connected: sending Logon
        20260929-09:05:53.478 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-09:05:53.478 INFO    session  emu42  store archived: outbound message store archived to data/msgstore/emu42.jsonl.20260929-090553 (Logon will carry 141=Y)
        20260929-09:05:53.479 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-09:05:53.478|98=0|108=30|141=Y|10=078|
        20260929-09:05:53.479 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:53.483 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:05:53.482|98=0|108=30|141=Y|10=073|
        20260929-09:05:53.487 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:05:53.487 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-09:05:53.490 IN   seq=2    35=5  8=FIX.4.2|9=78|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:53.490|58=bye from emulator|10=066|
        20260929-09:05:53.498 INFO    session  emu42  logout received: bye from emulator
        20260929-09:05:53.498 OUT  seq=2    35=5  8=FIX.4.2|9=80|35=5|49=AGENT|56=ORDERECHO|34=2|52=20260929-09:05:53.498|58=Logout acknowledged|10=040|
        20260929-09:05:53.498 INFO    session  emu42  Disconnecting: Logout requested by counterparty
        20260929-09:05:53.500 INFO    session  emu42  State ACTIVE -> DISCONNECTED
        20260929-09:05:53.504 INFO    session  emu42  disconnected: next_out=3 next_in=3
        20260929-09:05:53.504 INFO    session  emu42  Connection closed, peer=127.0.0.1:55516
        20260929-09:05:53.504 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by counterparty) (their 58: "bye from emulator")
--- PASS: TestScenario1LogonTestRequestLogout (3.96s)
=== RUN   TestScenario2EmulatorTestRequest
    interop_test.go:98: emulator up: fix=55542 strict=55543 api=55544 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario2EmulatorTestRequest2141519072/001/emulator
    interop_test.go:101: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":2,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:05:54.652|112=TEST-1|10=102|","injected":false}]}
    interop_test.go:114: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
--- PASS: TestScenario2EmulatorTestRequest (1.10s)
=== RUN   TestScenario3EmulatorSeqGap
    interop_test.go:120: emulator up: fix=55558 strict=55559 api=55560 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario3EmulatorSeqGap1650623802/001/emulator
    interop_test.go:124: POST /sessions/agent42/inject/seq-gap -> {"skipped":3,"next_out":5}
    interop_test.go:126: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":5,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=5|52=20260929-09:05:55.581|112=TEST-1|10=107|","injected":false}]}
    interop_test.go:144: in sync: agent next_out=4 next_in=6 / emulator next_in=4 next_out=6 (ACTIVE)
    interop_test.go:145: emu42: TestRequest TEST-1 answered
    interop_test.go:146: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-2","sent":[{"seq":7,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=7|52=20260929-09:05:55.698|112=TEST-2|10=119|","injected":false}]}
    interop_test.go:151: in sync: agent next_out=6 next_in=8 / emulator next_in=6 next_out=8 (ACTIVE)
--- PASS: TestScenario3EmulatorSeqGap (1.15s)
=== RUN   TestScenario4AgentSkipOutboundSeq
    interop_test.go:157: emulator up: fix=55577 strict=55578 api=55579 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario4AgentSkipOutboundSeq2047644773/001/emulator
    interop_test.go:189: in sync: agent next_out=6 next_in=3 / emulator next_in=6 next_out=3 (ACTIVE)
    interop_test.go:190: emu44: TestRequest TEST-2 answered
    interop_test.go:191: in sync: agent next_out=7 next_in=4 / emulator next_in=7 next_out=4 (ACTIVE)
    interop_test.go:202: in sync: agent next_out=8 next_in=5 / emulator next_in=8 next_out=5 (ACTIVE)
--- PASS: TestScenario4AgentSkipOutboundSeq (1.05s)
=== RUN   TestScenario5ReconnectWithoutReset
    interop_test.go:208: emulator up: fix=55592 strict=55593 api=55594 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario5ReconnectWithoutReset2418067254/001/emulator
    interop_test.go:211: emu42: TestRequest TEST-1 answered
    interop_test.go:212: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:214: POST /sessions/agent42/disconnect -> {"disconnected":true}
    interop_test.go:240: in sync: agent next_out=4 next_in=4 / emulator next_in=4 next_out=4 (ACTIVE)
    interop_test.go:241: emu42: TestRequest TEST-2 answered
    interop_test.go:242: in sync: agent next_out=5 next_in=5 / emulator next_in=5 next_out=5 (ACTIVE)
--- PASS: TestScenario5ReconnectWithoutReset (1.65s)
=== RUN   TestScenario6WrongVersion
    interop_test.go:254: emulator up: fix=55608 strict=55609 api=55610 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario6WrongVersion2615450195/001/emulator
    interop_test.go:256: $ orderecho connect --session strict44  -> exit 1
        OrderEcho agent 0.3.0 (a3) - session strict44
          config         : orderecho.yaml
          route          : AGENT -> STRICTBRK  FIX.4.4  127.0.0.1:55609
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/strict44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090559.378.jsonl
          fix log        : logs/fix/strict44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:59.379 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=strict44 FIX.4.4 AGENT->STRICTBRK@127.0.0.1:55609 evidence=data/evidence/20260929-090559.378.jsonl
        20260929-09:05:59.379 INFO    session  strict44  Connecting to 127.0.0.1:55609
        20260929-09:05:59.380 INFO    session  strict44  Connected to 127.0.0.1:55609 (local 127.0.0.1:55618)
        20260929-09:05:59.390 INFO    session  strict44  connected: sending Logon
        20260929-09:05:59.390 INFO    session  strict44  seqnums reset: Logon will carry 141=Y
        20260929-09:05:59.390 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-09:05:59.389|98=0|108=30|141=Y|10=116|
        20260929-09:05:59.390 INFO    session  strict44  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:59.393 IN   seq=1    35=5  8=FIX.4.2|9=100|35=5|49=STRICTBRK|56=AGENT|34=1|52=20260929-09:05:59.392|58=Incorrect BeginString, expected FIX.4.2|10=118|
        20260929-09:05:59.398 WARNING session  strict44  logon refused: counterparty answered Logon with Logout: Incorrect BeginString, expected FIX.4.2
        20260929-09:05:59.399 OUT  seq=2    35=5  8=FIX.4.4|9=80|35=5|49=AGENT|56=STRICTBRK|34=2|52=20260929-09:05:59.398|58=Logout acknowledged|10=076|
        20260929-09:05:59.399 INFO    session  strict44  Disconnecting: Logon refused by counterparty: Incorrect BeginString, expected FIX.4.2
        20260929-09:05:59.404 INFO    session  strict44  disconnected: next_out=3 next_in=1
        20260929-09:05:59.404 INFO    session  strict44  State LOGON_SENT -> DISCONNECTED
        20260929-09:05:59.404 INFO    session  strict44  Connection closed, peer=127.0.0.1:55609
        20260929-09:05:59.404 INFO    session  engine  Shutdown complete
        >>> LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
        orderecho: LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
    interop_test.go:275: $ orderecho connect --session strict --duration 500ms  -> exit 0
        OrderEcho agent 0.3.0 (a3) - session strict
          config         : orderecho.yaml
          route          : AGENT -> STRICTBRK  FIX.4.2  127.0.0.1:55609
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/strict.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090559.416.jsonl
          fix log        : logs/fix/strict_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:05:59.416 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=strict FIX.4.2 AGENT->STRICTBRK@127.0.0.1:55609 evidence=data/evidence/20260929-090559.416.jsonl
        20260929-09:05:59.416 INFO    session  strict  Connecting to 127.0.0.1:55609
        20260929-09:05:59.417 INFO    session  strict  Connected to 127.0.0.1:55609 (local 127.0.0.1:55619)
        20260929-09:05:59.431 INFO    session  strict  connected: sending Logon
        20260929-09:05:59.431 INFO    session  strict  seqnums reset: Logon will carry 141=Y
        20260929-09:05:59.431 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-09:05:59.430|98=0|108=30|141=Y|10=101|
        20260929-09:05:59.431 INFO    session  strict  State DISCONNECTED -> LOGON_SENT
        20260929-09:05:59.435 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=STRICTBRK|56=AGENT|34=1|52=20260929-09:05:59.434|98=0|108=30|141=Y|10=105|
        20260929-09:05:59.442 INFO    session  strict  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:05:59.442 INFO    session  strict  State LOGON_SENT -> ACTIVE
        >>> Logged on to STRICTBRK as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        >>> Duration 500ms elapsed; logging out
        20260929-09:05:59.990 OUT  seq=2    35=5  8=FIX.4.2|9=94|35=5|49=AGENT|56=STRICTBRK|34=2|52=20260929-09:05:59.990|58=OrderEcho agent: duration elapsed|10=051|
        20260929-09:05:59.990 INFO    session  strict  logout initiated: OrderEcho agent: duration elapsed
        20260929-09:05:59.990 INFO    session  strict  State ACTIVE -> LOGOUT_SENT
        20260929-09:05:59.992 IN   seq=2    35=5  8=FIX.4.2|9=80|35=5|49=STRICTBRK|56=AGENT|34=2|52=20260929-09:05:59.992|58=Logout acknowledged|10=074|
        20260929-09:05:59.999 INFO    session  strict  logout confirmed: Logout acknowledged
        20260929-09:05:59.999 INFO    session  strict  Disconnecting: Logout confirmed
        20260929-09:06:00.004 INFO    session  strict  disconnected: next_out=3 next_in=3
        20260929-09:06:00.004 INFO    session  strict  State LOGOUT_SENT -> DISCONNECTED
        20260929-09:06:00.004 INFO    session  strict  Connection closed, peer=127.0.0.1:55609
        20260929-09:06:00.004 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
--- PASS: TestScenario6WrongVersion (1.45s)
=== RUN   TestScenario7UnknownCompIDs
    interop_test.go:283: emulator up: fix=55620 strict=55621 api=55622 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario7UnknownCompIDs1253839074/001/emulator
    interop_test.go:285: $ orderecho connect --session unknown  -> exit 1
        OrderEcho agent 0.3.0 (a3) - session unknown
          config         : orderecho.yaml
          route          : NOBODY -> ORDERECHO  FIX.4.2  127.0.0.1:55620
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/unknown.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090600.824.jsonl
          fix log        : logs/fix/unknown_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:06:00.824 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=unknown FIX.4.2 NOBODY->ORDERECHO@127.0.0.1:55620 evidence=data/evidence/20260929-090600.824.jsonl
        20260929-09:06:00.825 INFO    session  unknown  Connecting to 127.0.0.1:55620
        20260929-09:06:00.825 INFO    session  unknown  Connected to 127.0.0.1:55620 (local 127.0.0.1:55630)
        20260929-09:06:00.879 INFO    session  unknown  connected: sending Logon
        20260929-09:06:00.879 INFO    session  unknown  seqnums reset: Logon will carry 141=Y
        20260929-09:06:00.879 OUT  seq=1    35=A  8=FIX.4.2|9=76|35=A|49=NOBODY|56=ORDERECHO|34=1|52=20260929-09:06:00.878|98=0|108=30|141=Y|10=168|
        20260929-09:06:00.879 INFO    session  unknown  State DISCONNECTED -> LOGON_SENT
        20260929-09:06:00.880 WARNING session  unknown  connection closed by counterparty
        20260929-09:06:00.884 INFO    session  unknown  disconnected: next_out=2 next_in=1
        20260929-09:06:00.884 INFO    session  unknown  State LOGON_SENT -> DISCONNECTED
        20260929-09:06:00.884 INFO    session  unknown  Connection closed, peer=127.0.0.1:55620
        20260929-09:06:00.884 INFO    session  engine  Shutdown complete
        >>> counterparty at 127.0.0.1:55620 closed the connection without answering our Logon (no Logout, no reason given) - check sender_comp_id=NOBODY, target_comp_id=ORDERECHO, fix_version=FIX.4.2 and the port
        orderecho: counterparty at 127.0.0.1:55620 closed the connection without answering our Logon (no Logout, no reason given) - check sender_comp_id=NOBODY, target_comp_id=ORDERECHO, fix_version=FIX.4.2 and the port
--- PASS: TestScenario7UnknownCompIDs (0.95s)
=== RUN   TestScenario8ViewerReadsAgentLogs
    interop_test.go:306: emulator up: fix=55631 strict=55632 api=55633 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario8ViewerReadsAgentLogs3126380898/001/emulator
    interop_test.go:309: emu42: TestRequest TEST-1 answered
    interop_test.go:310: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":3,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:06:01.983|112=TEST-1|10=103|","injected":false}]}
    interop_test.go:313: POST /sessions/agent42/inject/seq-gap -> {"skipped":2,"next_out":6}
    interop_test.go:314: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-2","sent":[{"seq":6,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=6|52=20260929-09:06:01.999|112=TEST-2|10=114|","injected":false}]}
    interop_test.go:320: in sync: agent next_out=9 next_in=8 / emulator next_in=9 next_out=8 (ACTIVE)
    interop_test.go:321: emu42: TestRequest TEST-3 answered
    interop_test.go:322: in sync: agent next_out=10 next_in=9 / emulator next_in=10 next_out=9 (ACTIVE)
    interop_test.go:352: viewer stats:
        17 message(s) from 1 file(s)
          first: 2026-09-29T09:06:01.874000+00:00
          last : 2026-09-29T09:06:02.211000+00:00
        
        By session
          emu42  17
        
        By message type
          1 Test Request    5
          0 Heartbeat       4
          2 Resend Request  2
          4 Sequence Reset  2
          5 Logout          2
          A Logon           2
        
        By direction
          out  9
          in   8
        
        Rejects by reason
          (none)
        
        Other
          resend requests   2
          gap fills         2
          injected          0
          bad checksum      0
          bad body length   0
          unparseable lines 0
    interop_test.go:369: viewer view:
        20260929-09:06:01.874 <-- emu42              1 Logon                  141=Y 108=30
        20260929-09:06:01.878 --> emu42              1 Logon                  141=Y 108=30
        20260929-09:06:01.929 <-- emu42              2 Test Request           112=TEST-1
        20260929-09:06:01.931 --> emu42              2 Heartbeat              112=TEST-1
        20260929-09:06:01.983 --> emu42              3 Test Request           112=TEST-1
        20260929-09:06:01.991 <-- emu42              3 Heartbeat              112=TEST-1
        20260929-09:06:01.999 --> emu42              6 Test Request           112=TEST-2
        20260929-09:06:02.003 <-- emu42              4 Resend Request         7=4 16=5
        20260929-09:06:02.004 --> emu42              4 Sequence Reset         36=6 123=Y [POSSDUP]
        20260929-09:06:02.022 <-- emu42              5 Heartbeat              112=TEST-2
        20260929-09:06:02.080 <-- emu42              8 Test Request           112=TEST-2
        20260929-09:06:02.082 --> emu42              7 Resend Request         7=6 16=0
        20260929-09:06:02.086 <-- emu42              6 Sequence Reset         36=9 123=Y [POSSDUP]
        20260929-09:06:02.148 <-- emu42              9 Test Request           112=TEST-3
        20260929-09:06:02.150 --> emu42              8 Heartbeat              112=TEST-3
        20260929-09:06:02.208 <-- emu42             10 Logout                 58=viewer test done
        20260929-09:06:02.211 --> emu42              9 Logout                 58=Logout acknowledged
    interop_test.go:401: viewer stats on evidence:
        17 message(s) from 1 file(s)
          first: 2026-09-29T09:06:01.874000+00:00
          last : 2026-09-29T09:06:02.211000+00:00
        
        By session
          emu42  17
        
        By message type
          1 Test Request    5
          0 Heartbeat       4
          2 Resend Request  2
          4 Sequence Reset  2
          5 Logout          2
          A Logon           2
        
        By direction
          out  9
          in   8
        
        Rejects by reason
          (none)
        
        Other
          resend requests   2
          gap fills         2
          injected          0
          bad checksum      0
          bad body length   0
          unparseable lines 0
--- PASS: TestScenario8ViewerReadsAgentLogs (1.76s)
=== RUN   TestCtrlCLogsOutCleanly
    interop_test.go:421: emulator up: fix=55652 strict=55653 api=55654 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCtrlCLogsOutCleanly1386548238/001/emulator
    interop_test.go:433: CLI output:
        OrderEcho agent 0.3.0 (a3) - session emu44
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:55652
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-090603.843.jsonl
          fix log        : logs/fix/emu44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-09:06:03.843 INFO    session  engine  Startup: version=0.3.0 build=a3 config=orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:55652 evidence=data/evidence/20260929-090603.843.jsonl
        20260929-09:06:03.844 INFO    session  emu44  Connecting to 127.0.0.1:55652
        20260929-09:06:03.844 INFO    session  emu44  Connected to 127.0.0.1:55652 (local 127.0.0.1:55666)
        20260929-09:06:03.991 INFO    session  emu44  connected: sending Logon
        20260929-09:06:03.991 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
        20260929-09:06:03.991 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-09:06:03.991|98=0|108=30|141=Y|10=076|
        20260929-09:06:03.992 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
        20260929-09:06:03.996 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-09:06:03.995|98=0|108=30|141=Y|10=080|
        
        >>> Ctrl+C: logging out (Ctrl+C again to exit at once)
        20260929-09:06:04.000 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-09:06:04.000 INFO    session  emu44  State LOGON_SENT -> ACTIVE
        20260929-09:06:04.005 OUT  seq=2    35=5  8=FIX.4.4|9=90|35=5|49=AGENT|56=ORDERECHO|34=2|52=20260929-09:06:04.005|58=OrderEcho agent shutting down|10=174|
        20260929-09:06:04.005 INFO    session  emu44  logout initiated: OrderEcho agent shutting down
        20260929-09:06:04.005 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=3 next_in=2)
        20260929-09:06:04.007 IN   seq=2    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-09:06:04.007|58=Logout acknowledged|10=025|
        20260929-09:06:04.012 INFO    session  emu44  logout confirmed: Logout acknowledged
        20260929-09:06:04.012 INFO    session  emu44  Disconnecting: Logout confirmed
        20260929-09:06:04.016 INFO    session  emu44  disconnected: next_out=3 next_in=3
        20260929-09:06:04.016 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
        20260929-09:06:04.016 INFO    session  emu44  Connection closed, peer=127.0.0.1:55652
        20260929-09:06:04.016 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
--- PASS: TestCtrlCLogsOutCleanly (1.31s)
=== RUN   TestA2Scenario01FullFill
    orders_test.go:100: emulator up: fix=55671 strict=55672 api=55673 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario01FullFill1632884344/001/emulator
    orders_test.go:116: sent D IT-20260929-090604.829-emu428-1 buy 100 AAPL mkt 
    orders_test.go:126: sent D IT-20260929-090604.829-emu428-2 sell 250 CSCO lmt 99.50
    orders_test.go:128: VERDICT 1 full fill FIX.4.2 (AAPL mkt) IT-20260929-090604.829-emu428-1: FILLED PASS
    orders_test.go:129: VERDICT 1 full fill FIX.4.2 (CSCO lmt day) IT-20260929-090604.829-emu428-2: FILLED PASS
    orders_test.go:131: parity A2-1 FIX.4.2 / agent FIX log / IT-20260929-090604.829-emu428-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / agent evidence / IT-20260929-090604.829-emu428-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / emulator FIX log / IT-20260929-090604.829-emu428-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / emulator evidence / IT-20260929-090604.829-emu428-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / agent FIX log / IT-20260929-090604.829-emu428-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / agent evidence / IT-20260929-090604.829-emu428-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / emulator FIX log / IT-20260929-090604.829-emu428-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / emulator evidence / IT-20260929-090604.829-emu428-2: PASS (all PASS)
    orders_test.go:100: emulator up: fix=55682 strict=55683 api=55684 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario01FullFill1632884344/004/emulator
    orders_test.go:116: sent D IT-20260929-090606.920-emu449-1 buy 100 AAPL mkt 
    orders_test.go:126: sent D IT-20260929-090606.920-emu449-2 sell 250 CSCO lmt 99.50
    orders_test.go:128: VERDICT 1 full fill FIX.4.4 (AAPL mkt) IT-20260929-090606.920-emu449-1: FILLED PASS
    orders_test.go:129: VERDICT 1 full fill FIX.4.4 (CSCO lmt day) IT-20260929-090606.920-emu449-2: FILLED PASS
    orders_test.go:131: parity A2-1 FIX.4.4 / agent FIX log / IT-20260929-090606.920-emu449-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / agent evidence / IT-20260929-090606.920-emu449-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / emulator FIX log / IT-20260929-090606.920-emu449-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / emulator evidence / IT-20260929-090606.920-emu449-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / agent FIX log / IT-20260929-090606.920-emu449-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / agent evidence / IT-20260929-090606.920-emu449-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / emulator FIX log / IT-20260929-090606.920-emu449-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / emulator evidence / IT-20260929-090606.920-emu449-2: PASS (all PASS)
--- PASS: TestA2Scenario01FullFill (4.90s)
=== RUN   TestA2Scenario02Partials
    orders_test.go:100: emulator up: fix=55694 strict=55695 api=55696 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario02Partials1169420401/001/emulator
    orders_test.go:138: sent D IT-20260929-090609.934-emu4210-1 buy 1000 EFG lmt 10.00
    orders_test.go:150: VERDICT 2 partials EFG (left working) IT-20260929-090609.934-emu4210-1: PARTIALLY_FILLED PASS
    orders_test.go:152: parity A2-2 / agent FIX log / IT-20260929-090609.934-emu4210-1: PASS (all PASS)
    orders_test.go:152: parity A2-2 / agent evidence / IT-20260929-090609.934-emu4210-1: PASS (all PASS)
    orders_test.go:152: parity A2-2 / emulator FIX log / IT-20260929-090609.934-emu4210-1: PASS (all PASS)
    orders_test.go:152: parity A2-2 / emulator evidence / IT-20260929-090609.934-emu4210-1: PASS (all PASS)
--- PASS: TestA2Scenario02Partials (3.61s)
=== RUN   TestA2Scenario03OddLots
    orders_test.go:100: emulator up: fix=55707 strict=55708 api=55709 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario03OddLots204739781/001/emulator
    orders_test.go:158: sent D IT-20260929-090613.336-emu4411-1 buy 1000 NOK mkt 
    orders_test.go:176: VERDICT 3 odd lots NOK 1/2/3/405/589 IT-20260929-090613.336-emu4411-1: FILLED PASS
    orders_test.go:178: parity A2-3 / agent FIX log / IT-20260929-090613.336-emu4411-1: PASS (all PASS)
    orders_test.go:178: parity A2-3 / agent evidence / IT-20260929-090613.336-emu4411-1: PASS (all PASS)
    orders_test.go:178: parity A2-3 / emulator FIX log / IT-20260929-090613.336-emu4411-1: PASS (all PASS)
    orders_test.go:178: parity A2-3 / emulator evidence / IT-20260929-090613.336-emu4411-1: PASS (all PASS)
--- PASS: TestA2Scenario03OddLots (3.69s)
=== RUN   TestA2Scenario04HoldReplaceCancel
    orders_test.go:100: emulator up: fix=55718 strict=55719 api=55720 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario04HoldReplaceCancel3906881530/001/emulator
    orders_test.go:185: sent D IT-20260929-090617.028-emu4212-1 buy 500 ZWZZT lmt 10.00
    orders_test.go:210: VERDICT 4 hold/replace/cancel ZWZZT FIX.4.2 IT-20260929-090617.028-emu4212-1: CANCELED PASS
    orders_test.go:212: parity A2-4 FIX.4.2 / agent FIX log / IT-20260929-090617.028-emu4212-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.2 / agent evidence / IT-20260929-090617.028-emu4212-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.2 / emulator FIX log / IT-20260929-090617.028-emu4212-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.2 / emulator evidence / IT-20260929-090617.028-emu4212-1: PASS (all PASS)
    orders_test.go:100: emulator up: fix=55729 strict=55730 api=55731 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario04HoldReplaceCancel3906881530/004/emulator
    orders_test.go:185: sent D IT-20260929-090617.871-emu4413-1 buy 500 ZWZZT lmt 10.00
    orders_test.go:210: VERDICT 4 hold/replace/cancel ZWZZT FIX.4.4 IT-20260929-090617.871-emu4413-1: CANCELED PASS
    orders_test.go:212: parity A2-4 FIX.4.4 / agent FIX log / IT-20260929-090617.871-emu4413-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.4 / agent evidence / IT-20260929-090617.871-emu4413-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.4 / emulator FIX log / IT-20260929-090617.871-emu4413-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.4 / emulator evidence / IT-20260929-090617.871-emu4413-1: PASS (all PASS)
--- PASS: TestA2Scenario04HoldReplaceCancel (2.98s)
=== RUN   TestA2Scenario05Rejects
    orders_test.go:100: emulator up: fix=55740 strict=55741 api=55742 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario05Rejects3259401929/001/emulator
    orders_test.go:220: sent D IT-20260929-090620.317-emu4214-1 buy 100 KO lmt 60.00
    orders_test.go:221: sent D IT-20260929-090620.317-emu4214-2 buy 100 AAPL lmt 500.00
    orders_test.go:230: VERDICT 5 rule reject KO IT-20260929-090620.317-emu4214-1: REJECTED PASS
    orders_test.go:231: VERDICT 5 band reject AAPL lmt 500 IT-20260929-090620.317-emu4214-2: REJECTED PASS
    orders_test.go:233: parity A2-5 / agent FIX log / IT-20260929-090620.317-emu4214-1: PASS (all PASS)
    orders_test.go:233: parity A2-5 / agent evidence / IT-20260929-090620.317-emu4214-1: PASS (all PASS)
    orders_test.go:233: parity A2-5 / emulator FIX log / IT-20260929-090620.317-emu4214-1: PASS (all PASS)
    orders_test.go:233: parity A2-5 / emulator evidence / IT-20260929-090620.317-emu4214-1: PASS (all PASS)
    orders_test.go:233: parity A2-5 / agent FIX log / IT-20260929-090620.317-emu4214-2: PASS (all PASS)
    orders_test.go:233: parity A2-5 / agent evidence / IT-20260929-090620.317-emu4214-2: PASS (all PASS)
    orders_test.go:233: parity A2-5 / emulator FIX log / IT-20260929-090620.317-emu4214-2: PASS (all PASS)
    orders_test.go:233: parity A2-5 / emulator evidence / IT-20260929-090620.317-emu4214-2: PASS (all PASS)
    orders_test.go:237: sent D IT-20260929-090620.741-strict15-1 buy 100 AAPL mkt 
    orders_test.go:242: VERDICT 5 strict broker rejects all IT-20260929-090620.741-strict15-1: REJECTED PASS
    orders_test.go:244: parity A2-5 strict / agent FIX log / IT-20260929-090620.741-strict15-1: PASS (all PASS)
    orders_test.go:244: parity A2-5 strict / agent evidence / IT-20260929-090620.741-strict15-1: PASS (all PASS)
    orders_test.go:244: parity A2-5 strict / emulator FIX log / IT-20260929-090620.741-strict15-1: PASS (all PASS)
    orders_test.go:244: parity A2-5 strict / emulator evidence / IT-20260929-090620.741-strict15-1: PASS (all PASS)
--- PASS: TestA2Scenario05Rejects (2.06s)
=== RUN   TestA2Scenario06UnsolicitedCancel
    orders_test.go:100: emulator up: fix=55755 strict=55756 api=55757 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario06UnsolicitedCancel3456316531/001/emulator
    orders_test.go:250: sent D IT-20260929-090622.071-emu4416-1 buy 100 HON lmt 10.00
    orders_test.go:255: VERDICT 6 unsolicited cancel HON IT-20260929-090622.071-emu4416-1: CANCELED PASS
    orders_test.go:257: parity A2-6 / agent FIX log / IT-20260929-090622.071-emu4416-1: PASS (all PASS)
    orders_test.go:257: parity A2-6 / agent evidence / IT-20260929-090622.071-emu4416-1: PASS (all PASS)
    orders_test.go:257: parity A2-6 / emulator FIX log / IT-20260929-090622.071-emu4416-1: PASS (all PASS)
    orders_test.go:257: parity A2-6 / emulator evidence / IT-20260929-090622.071-emu4416-1: PASS (all PASS)
--- PASS: TestA2Scenario06UnsolicitedCancel (1.67s)
=== RUN   TestA2Scenario07ManualFills
    orders_test.go:100: emulator up: fix=55766 strict=55767 api=55768 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario07ManualFills2046378825/001/emulator
    orders_test.go:263: sent D IT-20260929-090623.745-emu4217-1 buy 1000 ZWZZT lmt 10.00
    orders_test.go:266: POST /orders/O-20260929-090623-1/fill -> {"session":"agent42","order":{"order_id":"O-20260929-090623-1","cl_ord_id":"IT-20260929-090623.745-emu4217-1","symbol":"ZWZZT","side":"1","order_qty":"1000","cum_qty":"300","leaves_qty":"700","avg_px":"10.0000","ord_status":"1","price_source":"limit","rule_name":"nasdaq-test-hold"},"sent":[{"seq":3,"msg_type":"8","raw":"8=FIX.4.2|9=257|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-09:06:23.901|37=O-20260929-090623-1|11=IT-20260929-090623.745-emu4217-1|17=E-20260929-090623-2|20=0|150=1|39=1|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=300|31=10.00|151=700|14=300|6=10.0000|60=20260929-09:06:23.900|10=188|","injected":false}]}
    orders_test.go:268: POST /orders/O-20260929-090623-1/fill -> {"session":"agent42","order":{"order_id":"O-20260929-090623-1","cl_ord_id":"IT-20260929-090623.745-emu4217-1","symbol":"ZWZZT","side":"1","order_qty":"1000","cum_qty":"500","leaves_qty":"500","avg_px":"10.4000","ord_status":"1","price_source":"limit","rule_name":"nasdaq-test-hold"},"sent":[{"seq":4,"msg_type":"8","raw":"8=FIX.4.2|9=257|35=8|49=ORDERECHO|56=AGENT|34=4|52=20260929-09:06:23.909|37=O-20260929-090623-1|11=IT-20260929-090623.745-emu4217-1|17=E-20260929-090623-3|20=0|150=1|39=1|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=200|31=11.00|151=500|14=500|6=10.4000|60=20260929-09:06:23.908|10=210|","injected":false}]}
    orders_test.go:277: VERDICT 7 manual fills 300@10 + 200@11 IT-20260929-090623.745-emu4217-1: PARTIALLY_FILLED PASS
    orders_test.go:279: parity A2-7 / agent FIX log / IT-20260929-090623.745-emu4217-1: PASS (all PASS)
    orders_test.go:279: parity A2-7 / agent evidence / IT-20260929-090623.745-emu4217-1: PASS (all PASS)
    orders_test.go:279: parity A2-7 / emulator FIX log / IT-20260929-090623.745-emu4217-1: PASS (all PASS)
    orders_test.go:279: parity A2-7 / emulator evidence / IT-20260929-090623.745-emu4217-1: PASS (all PASS)
--- PASS: TestA2Scenario07ManualFills (1.15s)
=== RUN   TestA2Scenario08UnknownTagTolerated
    orders_test.go:100: emulator up: fix=55779 strict=55780 api=55781 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario08UnknownTagTolerated3122874606/001/emulator
    orders_test.go:285: POST /sessions/agent44/inject/next -> {"queued":{"id":1,"msg_type":"8","set":{"9999":"FOO"},"remove":[],"corrupt_checksum":false,"count":1,"remaining":1}}
    orders_test.go:286: sent D IT-20260929-090624.893-emu4418-1 buy 100 AAPL mkt 
    orders_test.go:303: VERDICT 8 ER with 9999=FOO IT-20260929-090624.893-emu4418-1: FILLED PASS
    orders_test.go:305: parity A2-8 / agent FIX log / IT-20260929-090624.893-emu4418-1: PASS (all PASS)
    orders_test.go:305: parity A2-8 / agent evidence / IT-20260929-090624.893-emu4418-1: PASS (all PASS)
    orders_test.go:305: parity A2-8 / emulator FIX log / IT-20260929-090624.893-emu4418-1: PASS (all PASS)
    orders_test.go:305: parity A2-8 / emulator evidence / IT-20260929-090624.893-emu4418-1: PASS (all PASS)
--- PASS: TestA2Scenario08UnknownTagTolerated (1.65s)
=== RUN   TestA2Scenario09DuplicateClOrdID
    orders_test.go:100: emulator up: fix=55791 strict=55792 api=55793 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario09DuplicateClOrdID2182788600/001/emulator
    orders_test.go:311: sent D IT-20260929-090626.542-emu4219-1 buy 500 ZWZZT lmt 10.00
    orders_test.go:345: VERDICT 9 duplicate ClOrdID via SendRaw IT-20260929-090626.542-emu4219-1: NEW PASS
--- PASS: TestA2Scenario09DuplicateClOrdID (1.05s)
=== RUN   TestA2Scenario10TestRequestBehindGap
    orders_test.go:100: emulator up: fix=55802 strict=55803 api=55804 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario10TestRequestBehindGap1931361390/001/emulator
    orders_test.go:358: POST /sessions/agent42/inject/seq-gap -> {"skipped":3,"next_out":5}
    orders_test.go:359: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":5,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=5|52=20260929-09:06:27.717|112=TEST-1|10=108|","injected":false}]}
    orders_test.go:382: in sync: agent next_out=4 next_in=6 / emulator next_in=4 next_out=6 (ACTIVE)
    orders_test.go:383: sent D IT-20260929-090627.593-emu4220-1 buy 100 AAPL mkt 
    orders_test.go:385: VERDICT 10 TestRequest behind gap, then AAPL IT-20260929-090627.593-emu4220-1: FILLED PASS
    orders_test.go:387: parity A2-10 / agent FIX log / IT-20260929-090627.593-emu4220-1: PASS (all PASS)
    orders_test.go:387: parity A2-10 / agent evidence / IT-20260929-090627.593-emu4220-1: PASS (all PASS)
    orders_test.go:387: parity A2-10 / emulator FIX log / IT-20260929-090627.593-emu4220-1: PASS (all PASS)
    orders_test.go:387: parity A2-10 / emulator evidence / IT-20260929-090627.593-emu4220-1: PASS (all PASS)
--- PASS: TestA2Scenario10TestRequestBehindGap (1.76s)
=== RUN   TestA2Scenario11CancelUnknown
    orders_test.go:100: emulator up: fix=55818 strict=55819 api=55820 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario11CancelUnknown2052680262/001/emulator
    orders_test.go:420: parity A2-11 / agent FIX log / RAWF-dlroiz11a1bs: PASS (all PASS)
    orders_test.go:420: parity A2-11 / agent evidence / RAWF-dlroiz11a1bs: PASS (all PASS)
    orders_test.go:420: parity A2-11 / emulator FIX log / RAWF-dlroiz11a1bs: PASS (all PASS)
    orders_test.go:420: parity A2-11 / emulator evidence / RAWF-dlroiz11a1bs: PASS (all PASS)
--- PASS: TestA2Scenario11CancelUnknown (1.06s)
=== RUN   TestParityTampered
    orders_test.go:100: emulator up: fix=55829 strict=55830 api=55831 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestParityTampered3551345891/001/emulator
    parity_test.go:104: sent D IT-20260929-090630.412-emu4422-1 buy 1000 NOK mkt 
    parity_test.go:105: sent D IT-20260929-090630.412-emu4422-2 buy 500 ZWZZT lmt 10.00
    parity_test.go:117: VERDICT tamper base: NOK odd lots (4.4) IT-20260929-090630.412-emu4422-1: FILLED PASS
    parity_test.go:118: VERDICT tamper base: ZWZZT replace/cancel (4.4) IT-20260929-090630.412-emu4422-2: CANCELED PASS
    parity_test.go:260: parity tampered agent FIX log: cum_qty_monotonic (IT-20260929-090630.412-emu4422-1): FAIL (cum_qty_monotonic=FAIL working_quantities=FAIL)
    parity_test.go:260: parity tampered agent FIX log: working_quantities (IT-20260929-090630.412-emu4422-1): FAIL (working_quantities=FAIL)
    parity_test.go:260: parity tampered agent FIX log: terminal_quantities (IT-20260929-090630.412-emu4422-1): FAIL (terminal_quantities=FAIL)
    parity_test.go:260: parity tampered agent FIX log: fill_quantities_sum (IT-20260929-090630.412-emu4422-1): FAIL (fill_quantities_sum=FAIL)
    parity_test.go:260: parity tampered agent FIX log: avg_px (IT-20260929-090630.412-emu4422-1): FAIL (avg_px=FAIL)
    parity_test.go:260: parity tampered agent FIX log: exec_ids_unique (IT-20260929-090630.412-emu4422-1): FAIL (exec_ids_unique=FAIL)
    parity_test.go:260: parity tampered agent FIX log: order_id_constant (IT-20260929-090630.412-emu4422-1): FAIL (order_id_constant=FAIL)
    parity_test.go:260: parity tampered agent FIX log: nothing_after_terminal (IT-20260929-090630.412-emu4422-1): FAIL (fill_quantities_sum=FAIL nothing_after_terminal=FAIL)
    parity_test.go:260: parity tampered agent FIX log: version_rules (IT-20260929-090630.412-emu4422-1): FAIL (version_rules=FAIL)
    parity_test.go:260: parity tampered agent FIX log: version_rules (IT-20260929-090630.412-emu4422-2): FAIL (version_rules=FAIL)
    parity_test.go:260: parity tampered agent FIX log: requests_answered (IT-20260929-090630.412-emu4422-2): WARN (requests_answered=WARN)
    parity_test.go:260: parity tampered agent FIX log: framing_intact (IT-20260929-090630.412-emu4422-2): WARN (framing_intact=WARN)
    parity_test.go:260: parity tampered agent evidence: avg_px (IT-20260929-090630.412-emu4422-1): FAIL (avg_px=FAIL)
--- PASS: TestParityTampered (3.69s)
=== RUN   TestParityKnownDivergences
    parity_test.go:354: divergence 1: own Reject counted as an answer                          go=WARN py=PASS [requests_answered: go WARN, py PASS]
    parity_test.go:354: divergence 2: tag 110= truncates the message                           go=PASS py=WARN [framing_intact: go PASS, py WARN]
    parity_test.go:354: divergence 3: non-ASCII value breaks the checksum                      go=PASS py=WARN [framing_intact: go PASS, py WARN]
--- PASS: TestParityKnownDivergences (0.06s)
PASS

PARITY SUMMARY: 81 case(s) compared, 81 agree, 0 disagree, 7 documented divergence(s) as expected
  AGREE    CLI session / agent FIX log / OE-20260929-090549.026-1                 go=PASS py=PASS [all PASS]
  AGREE    CLI session / emulator FIX log / OE-20260929-090549.026-1              go=PASS py=PASS [all PASS]
  AGREE    CLI session / agent FIX log / OE-20260929-090549.026-2                 go=PASS py=PASS [all PASS]
  AGREE    CLI session / emulator FIX log / OE-20260929-090549.026-2              go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent FIX log / IT-20260929-090604.829-emu428-1         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent evidence / IT-20260929-090604.829-emu428-1        go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator FIX log / IT-20260929-090604.829-emu428-1      go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator evidence / IT-20260929-090604.829-emu428-1     go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent FIX log / IT-20260929-090604.829-emu428-2         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent evidence / IT-20260929-090604.829-emu428-2        go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator FIX log / IT-20260929-090604.829-emu428-2      go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator evidence / IT-20260929-090604.829-emu428-2     go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent FIX log / IT-20260929-090606.920-emu449-1         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent evidence / IT-20260929-090606.920-emu449-1        go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator FIX log / IT-20260929-090606.920-emu449-1      go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator evidence / IT-20260929-090606.920-emu449-1     go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent FIX log / IT-20260929-090606.920-emu449-2         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent evidence / IT-20260929-090606.920-emu449-2        go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator FIX log / IT-20260929-090606.920-emu449-2      go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator evidence / IT-20260929-090606.920-emu449-2     go=PASS py=PASS [all PASS]
  AGREE    A2-2 / agent FIX log / IT-20260929-090609.934-emu4210-1                go=PASS py=PASS [all PASS]
  AGREE    A2-2 / agent evidence / IT-20260929-090609.934-emu4210-1               go=PASS py=PASS [all PASS]
  AGREE    A2-2 / emulator FIX log / IT-20260929-090609.934-emu4210-1             go=PASS py=PASS [all PASS]
  AGREE    A2-2 / emulator evidence / IT-20260929-090609.934-emu4210-1            go=PASS py=PASS [all PASS]
  AGREE    A2-3 / agent FIX log / IT-20260929-090613.336-emu4411-1                go=PASS py=PASS [all PASS]
  AGREE    A2-3 / agent evidence / IT-20260929-090613.336-emu4411-1               go=PASS py=PASS [all PASS]
  AGREE    A2-3 / emulator FIX log / IT-20260929-090613.336-emu4411-1             go=PASS py=PASS [all PASS]
  AGREE    A2-3 / emulator evidence / IT-20260929-090613.336-emu4411-1            go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / agent FIX log / IT-20260929-090617.028-emu4212-1        go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / agent evidence / IT-20260929-090617.028-emu4212-1       go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / emulator FIX log / IT-20260929-090617.028-emu4212-1     go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / emulator evidence / IT-20260929-090617.028-emu4212-1    go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / agent FIX log / IT-20260929-090617.871-emu4413-1        go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / agent evidence / IT-20260929-090617.871-emu4413-1       go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / emulator FIX log / IT-20260929-090617.871-emu4413-1     go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / emulator evidence / IT-20260929-090617.871-emu4413-1    go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent FIX log / IT-20260929-090620.317-emu4214-1                go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent evidence / IT-20260929-090620.317-emu4214-1               go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator FIX log / IT-20260929-090620.317-emu4214-1             go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator evidence / IT-20260929-090620.317-emu4214-1            go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent FIX log / IT-20260929-090620.317-emu4214-2                go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent evidence / IT-20260929-090620.317-emu4214-2               go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator FIX log / IT-20260929-090620.317-emu4214-2             go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator evidence / IT-20260929-090620.317-emu4214-2            go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / agent FIX log / IT-20260929-090620.741-strict15-1        go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / agent evidence / IT-20260929-090620.741-strict15-1       go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / emulator FIX log / IT-20260929-090620.741-strict15-1     go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / emulator evidence / IT-20260929-090620.741-strict15-1    go=PASS py=PASS [all PASS]
  AGREE    A2-6 / agent FIX log / IT-20260929-090622.071-emu4416-1                go=PASS py=PASS [all PASS]
  AGREE    A2-6 / agent evidence / IT-20260929-090622.071-emu4416-1               go=PASS py=PASS [all PASS]
  AGREE    A2-6 / emulator FIX log / IT-20260929-090622.071-emu4416-1             go=PASS py=PASS [all PASS]
  AGREE    A2-6 / emulator evidence / IT-20260929-090622.071-emu4416-1            go=PASS py=PASS [all PASS]
  AGREE    A2-7 / agent FIX log / IT-20260929-090623.745-emu4217-1                go=PASS py=PASS [all PASS]
  AGREE    A2-7 / agent evidence / IT-20260929-090623.745-emu4217-1               go=PASS py=PASS [all PASS]
  AGREE    A2-7 / emulator FIX log / IT-20260929-090623.745-emu4217-1             go=PASS py=PASS [all PASS]
  AGREE    A2-7 / emulator evidence / IT-20260929-090623.745-emu4217-1            go=PASS py=PASS [all PASS]
  AGREE    A2-8 / agent FIX log / IT-20260929-090624.893-emu4418-1                go=PASS py=PASS [all PASS]
  AGREE    A2-8 / agent evidence / IT-20260929-090624.893-emu4418-1               go=PASS py=PASS [all PASS]
  AGREE    A2-8 / emulator FIX log / IT-20260929-090624.893-emu4418-1             go=PASS py=PASS [all PASS]
  AGREE    A2-8 / emulator evidence / IT-20260929-090624.893-emu4418-1            go=PASS py=PASS [all PASS]
  DIVERGE  A2-9 / agent FIX log / IT-20260929-090626.542-emu4219-1                go=PASS py=FAIL [order_id_constant: go PASS, py FAIL] (documented, expected: A3 3.2 duplicate-reject chain split; Python not yet updated)
  DIVERGE  A2-9 / agent evidence / IT-20260929-090626.542-emu4219-1               go=PASS py=FAIL [order_id_constant: go PASS, py FAIL] (documented, expected: A3 3.2 duplicate-reject chain split; Python not yet updated)
  DIVERGE  A2-9 / emulator FIX log / IT-20260929-090626.542-emu4219-1             go=PASS py=FAIL [order_id_constant: go PASS, py FAIL] (documented, expected: A3 3.2 duplicate-reject chain split; Python not yet updated)
  DIVERGE  A2-9 / emulator evidence / IT-20260929-090626.542-emu4219-1            go=PASS py=FAIL [order_id_constant: go PASS, py FAIL] (documented, expected: A3 3.2 duplicate-reject chain split; Python not yet updated)
  AGREE    A2-10 / agent FIX log / IT-20260929-090627.593-emu4220-1               go=PASS py=PASS [all PASS]
  AGREE    A2-10 / agent evidence / IT-20260929-090627.593-emu4220-1              go=PASS py=PASS [all PASS]
  AGREE    A2-10 / emulator FIX log / IT-20260929-090627.593-emu4220-1            go=PASS py=PASS [all PASS]
  AGREE    A2-10 / emulator evidence / IT-20260929-090627.593-emu4220-1           go=PASS py=PASS [all PASS]
  AGREE    A2-11 / agent FIX log / RAWF-dlroiz11a1bs                              go=PASS py=PASS [all PASS]
  AGREE    A2-11 / agent evidence / RAWF-dlroiz11a1bs                             go=PASS py=PASS [all PASS]
  AGREE    A2-11 / emulator FIX log / RAWF-dlroiz11a1bs                           go=PASS py=PASS [all PASS]
  AGREE    A2-11 / emulator evidence / RAWF-dlroiz11a1bs                          go=PASS py=PASS [all PASS]
  AGREE    tampered agent FIX log: cum_qty_monotonic (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [cum_qty_monotonic=FAIL working_quantities=FAIL]
  AGREE    tampered agent FIX log: working_quantities (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [working_quantities=FAIL]
  AGREE    tampered agent FIX log: terminal_quantities (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [terminal_quantities=FAIL]
  AGREE    tampered agent FIX log: fill_quantities_sum (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [fill_quantities_sum=FAIL]
  AGREE    tampered agent FIX log: avg_px (IT-20260929-090630.412-emu4422-1)      go=FAIL py=FAIL [avg_px=FAIL]
  AGREE    tampered agent FIX log: exec_ids_unique (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [exec_ids_unique=FAIL]
  AGREE    tampered agent FIX log: order_id_constant (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    tampered agent FIX log: nothing_after_terminal (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [fill_quantities_sum=FAIL nothing_after_terminal=FAIL]
  AGREE    tampered agent FIX log: version_rules (IT-20260929-090630.412-emu4422-1) go=FAIL py=FAIL [version_rules=FAIL]
  AGREE    tampered agent FIX log: version_rules (IT-20260929-090630.412-emu4422-2) go=FAIL py=FAIL [version_rules=FAIL]
  AGREE    tampered agent FIX log: requests_answered (IT-20260929-090630.412-emu4422-2) go=WARN py=WARN [requests_answered=WARN]
  AGREE    tampered agent FIX log: framing_intact (IT-20260929-090630.412-emu4422-2) go=WARN py=WARN [framing_intact=WARN]
  AGREE    tampered agent evidence: avg_px (IT-20260929-090630.412-emu4422-1)     go=FAIL py=FAIL [avg_px=FAIL]
  DIVERGE  divergence 1: own Reject counted as an answer                          go=WARN py=PASS [requests_answered: go WARN, py PASS] (documented, expected)
  DIVERGE  divergence 2: tag 110= truncates the message                           go=PASS py=WARN [framing_intact: go PASS, py WARN] (documented, expected)
  DIVERGE  divergence 3: non-ASCII value breaks the checksum                      go=PASS py=WARN [framing_intact: go PASS, py WARN] (documented, expected)

SCENARIO VERDICTS (Go, live):
  A3-1 cert FIX 4.2 emulator (no attest)       exit 7  PASS 46, PENDING 11, N/A 11
  A3-1 cert FIX 4.2 emulator (attested)        exit 0  PASS 54, N/A 14
  A3-2 cert FIX 4.4 emulator                   exit 7  PASS 46, PENDING 11, N/A 11
  A3-3 cert strict broker (negative control)   exit 5  PASS 24, FAIL 22, PENDING 11, N/A 11
  A3-4 cert generic target (assisted BLOCKED)  exit 7  PASS 1, BLOCKED 8
  A3-5 broken case (FAIL + reason)             exit 5  step 3 (expect): timed out after 3s waiting for ord_status=FILLED from the counterparty; last relevant message: 35=8 seq=2 11=OE-20260929-090537.940-1 37=O-20260929-090537-1 150=0 39=0 32=0 14=0 151=100 6=0.0000
  A3-5 emulator killed mid-case                exit 3  B.2/B.3 ERROR (session dropped)
  1 full fill FIX.4.2 (AAPL mkt)               IT-20260929-090604.829-emu428-1 FILLED            PASS 
  1 full fill FIX.4.2 (CSCO lmt day)           IT-20260929-090604.829-emu428-2 FILLED            PASS 
  1 full fill FIX.4.4 (AAPL mkt)               IT-20260929-090606.920-emu449-1 FILLED            PASS 
  1 full fill FIX.4.4 (CSCO lmt day)           IT-20260929-090606.920-emu449-2 FILLED            PASS 
  2 partials EFG (left working)                IT-20260929-090609.934-emu4210-1 PARTIALLY_FILLED  PASS 
  3 odd lots NOK 1/2/3/405/589                 IT-20260929-090613.336-emu4411-1 FILLED            PASS 
  4 hold/replace/cancel ZWZZT FIX.4.2          IT-20260929-090617.028-emu4212-1 CANCELED          PASS 
  4 hold/replace/cancel ZWZZT FIX.4.4          IT-20260929-090617.871-emu4413-1 CANCELED          PASS 
  5 rule reject KO                             IT-20260929-090620.317-emu4214-1 REJECTED          PASS 
  5 band reject AAPL lmt 500                   IT-20260929-090620.317-emu4214-2 REJECTED          PASS 
  5 strict broker rejects all                  IT-20260929-090620.741-strict15-1 REJECTED          PASS 
  6 unsolicited cancel HON                     IT-20260929-090622.071-emu4416-1 CANCELED          PASS 
  7 manual fills 300@10 + 200@11               IT-20260929-090623.745-emu4217-1 PARTIALLY_FILLED  PASS 
  8 ER with 9999=FOO                           IT-20260929-090624.893-emu4418-1 FILLED            PASS 
  9 duplicate ClOrdID via SendRaw              IT-20260929-090626.542-emu4219-1 NEW               PASS 
  10 TestRequest behind gap, then AAPL         IT-20260929-090627.593-emu4220-1 FILLED            PASS 
  11 cancel unknown ClOrdID via SendRaw        RAWF-dlroiz11a1bs              (no order)        PASS
  tamper base: NOK odd lots (4.4)              IT-20260929-090630.412-emu4422-1 FILLED            PASS 
  tamper base: ZWZZT replace/cancel (4.4)      IT-20260929-090630.412-emu4422-2 CANCELED          PASS 
ok  	github.com/danielgavin-code/OrderEcho/internal/interop	212.460s
```
</details>

## 3. Real run

### 3.0 The emulator was yours (again)

PID 62856 was listening on 9878, 9879 and 8090: `python orderecho_Main.py --config config/orderecho_multi.yaml`, started 03:54 local from `../OrderEchoFixEmulator` in a terminal, with live pricing. That is the configuration §10.3 asks for, so the three runs below went to it.
- **I did not stop it**, because it isn't mine; it is still running.
- **Its runtime files now include these runs' traffic:** logs, `data/evidence/20260929-075504.jsonl`, seqnums, and the msgstore, which it archived on our resets.

No emulator source or config file was changed. Nothing I started is left running.

All three runs used `bin/orderecho cert run --suite certs/order_entry_fix42.yaml --target certs/targets/emulator.yaml`, from the repo with the shipped `config/orderecho.yaml` (HeartBtInt 30).

### 3.1 `--session emu42` (no attestations) → exit 7
```
order-entry-fix42 — Order Entry Certification — FIX 4.2 US Equities
target orderecho-emulator, session emu42 (FIX.4.2), run 20260929-085859.259, agent 0.3.0 (a3)

ID   TITLE                                             REQ  MODE      STATUS   REASON
1.1  Certification environment details and documenta…  req  manual    PENDING  needs a human attestation: Confirm the cert host, port and the venue's documentation package were received.
1.2  TCP connectivity to the cert host                 req  auto      PASS     
1.3  Outbound firewall permits FIX egress              req  auto      PASS     
1.4  Inbound rules for a different response IP/port    opt  manual    PENDING  needs a human attestation: Confirm whether the venue pushes responses from a different IP/port range, and that inbound rules allow it (or N/A).
1.5  CompIDs, password and sequence reset policy col…  req  manual    PENDING  needs a human attestation: Confirm SenderCompID, TargetCompID, session password and the venue's sequence reset policy were provisioned and recorded.
1.6  TLS vs clear-text requirement confirmed           req  manual    PENDING  needs a human attestation: Confirm with the venue's onboarding team whether TLS is required (this agent speaks clear-text TCP).
1.7  UAT endpoint confirmed                            req  manual    PENDING  needs a human attestation: Confirm the configured host/port is the venue's UAT (certification) endpoint, not production.
2.1  BeginString configured                            req  auto      PASS     
2.2  SenderCompID / TargetCompID as provisioned        req  auto      PASS     
2.3  HeartBtInt configured                             req  auto      PASS     
2.4  EncryptMethod = 0                                 req  auto      PASS     
2.5  ResetOnLogon for the cert session                 opt  auto      PASS     
2.6  Clean sequence state before the cert              req  auto      PASS     
2.7  ResetSeqNumFlag=Y accepted on reconnect           req  auto      PASS     
3.1  Logon acknowledged                                req  auto      PASS     
3.2  Bidirectional heartbeats at HeartBtInt            req  auto      PASS     
3.3  TestRequest answered with matching Heartbeat      req  auto      PASS     
3.4  Exchange-initiated TestRequest answered           req  assisted  PASS     
3.5  ResendRequest replayed with PossDupFlag=Y         req  auto      PASS     
3.6  Sequence gap bridged with SequenceReset-GapFill   req  auto      PASS     
3.7  Graceful Logout acknowledged                      req  auto      PASS     
3.8  Recovery after a disconnect without Logout        req  auto      PASS     
4.1  New Order Single — Market Buy                     req  auto      PASS     
4.2  New Order Single — Market Sell                    req  auto      PASS     
4.3  New Order Single — Limit Buy                      req  auto      PASS     
4.4  New Order Single — Limit Sell                     req  auto      PASS     
4.5  IOC order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
4.6  FOK order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
4.7  Day order                                         req  auto      PASS     
4.8  GTC order persists across session restart         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
4.9  GTX order                                         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
5.1  Pending New before the acknowledgement            opt  auto      N/A      target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
5.2  New acknowledgement                               req  auto      PASS     
5.3  Partial fill                                      req  assisted  PASS     
5.4  Full fill                                         req  auto      PASS     
5.5  Done For Day                                      opt  assisted  N/A      target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
5.6  Expired                                           opt  assisted  N/A      target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
5.7  ExecIDs unique across the session                 req  auto      PASS     
5.8  AvgPx and CumQty accumulate across partial fills  req  assisted  PASS     
6.1  Order Cancel Request references ClOrdID and Ori…  req  auto      PASS     
6.2  Cancel acknowledgement                            req  auto      PASS     
6.3  Cancel Reject for a fully-filled order            req  auto      PASS     
6.4  Cancel Reject — too late to cancel (order pendi…  req  assisted  N/A      target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
6.5  Cancel/Replace — price only                       req  auto      PASS     
6.6  Cancel/Replace — quantity up                      req  auto      PASS     
6.7  Cancel/Replace — quantity down                    opt  auto      PASS     
6.8  Pending Replace then Replace ack                  opt  auto      N/A      target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
7.1  Missing required tag — Session Reject             req  auto      PASS     
7.2  Invalid MsgType — reject                          req  auto      PASS     
7.3  Invalid Symbol — reject                           req  auto      PASS     
7.4  Invalid Price — reject                            req  auto      PASS     
7.5  Invalid Side — reject with RefTagID=54            req  auto      PASS     
7.6  Invalid Account — reject                          opt  auto      N/A      target orderecho-emulator: Emulator does not validate Account (1)
7.7  Duplicate ClOrdID — reject                        req  auto      PASS     
7.8  BusinessMessageReject structure                   req  auto      PASS     warning: step 3 (assert_received): last received message: 35=j seq=66 45=62 372=ZZ 380=3 58=Not supported in this build; recommended tag(s) absent: 3…
8.1  Mid-session disconnect: ResendRequest / gap fil…  req  assisted  PASS     
8.2  PossDup resend of an order is not re-executed     req  auto      PASS     
8.3  Execution Reports replayed with PossDupFlag=Y     req  auto      PASS     
8.4  ResendRequest while Execution Reports are in fl…  opt  auto      PASS     
8.5  No duplicate ExecIDs on replay                    req  auto      PASS     
8.6  Client restart with sequence reset                req  auto      PASS     
8.7  Exchange restart: reconnect, gap fill, open ord…  opt  assisted  N/A      target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
9.1  All required tests passed                         req  manual    PENDING  needs a human attestation: Review results.json: every required case is PASS (or N/A with the venue's agreement).
9.2  Venue deviations documented                       req  manual    PENDING  needs a human attestation: Document the venue's deviations from FIX 4.2 seen during certification (warnings and N/A reasons are a starting point).
9.3  Written certification approval                    req  manual    PENDING  needs a human attestation: Obtain the venue's written certification approval.
9.4  Approval filed internally                         req  manual    PENDING  needs a human attestation: File the approval email in the internal tracking system with date and venue name.
9.5  Results shared with compliance and operations     opt  manual    PENDING  needs a human attestation: Share the certification results with compliance and operations.
9.6  Production cutover scheduled                      opt  manual    PENDING  needs a human attestation: Schedule the production cutover date and confirm it with the venue's go-live team.

all cases     : PASS 46, PENDING 11, N/A 11
required cases: PASS 43, PENDING 8, N/A 3
exit code     : 7
results       : data/certs/20260929-085859.259
exit 7
```

### 3.2 `--session emu42 --attest attest_emulator.yaml` → exit 0

The attestation file covers the 11 manual rows.
```
order-entry-fix42 — Order Entry Certification — FIX 4.2 US Equities
target orderecho-emulator, session emu42 (FIX.4.2), run 20260929-085954.753, agent 0.3.0 (a3)

ID   TITLE                                             REQ  MODE      STATUS  REASON
1.1  Certification environment details and documenta…  req  manual    PASS    attested pass by Cert operator: Emulator host/port are local; its docs site is the documentation package
1.2  TCP connectivity to the cert host                 req  auto      PASS    
1.3  Outbound firewall permits FIX egress              req  auto      PASS    
1.4  Inbound rules for a different response IP/port    opt  manual    N/A     attested na by Cert operator: The emulator answers on the same connection
1.5  CompIDs, password and sequence reset policy col…  req  manual    PASS    attested pass by Cert operator: AGENT -> ORDERECHO / STRICTBRK, no password, reset on logon
1.6  TLS vs clear-text requirement confirmed           req  manual    PASS    attested pass by Cert operator: Clear-text TCP on loopback
1.7  UAT endpoint confirmed                            req  manual    PASS    attested pass by Cert operator: The local emulator stands in for UAT
2.1  BeginString configured                            req  auto      PASS    
2.2  SenderCompID / TargetCompID as provisioned        req  auto      PASS    
2.3  HeartBtInt configured                             req  auto      PASS    
2.4  EncryptMethod = 0                                 req  auto      PASS    
2.5  ResetOnLogon for the cert session                 opt  auto      PASS    
2.6  Clean sequence state before the cert              req  auto      PASS    
2.7  ResetSeqNumFlag=Y accepted on reconnect           req  auto      PASS    
3.1  Logon acknowledged                                req  auto      PASS    
3.2  Bidirectional heartbeats at HeartBtInt            req  auto      PASS    
3.3  TestRequest answered with matching Heartbeat      req  auto      PASS    
3.4  Exchange-initiated TestRequest answered           req  assisted  PASS    
3.5  ResendRequest replayed with PossDupFlag=Y         req  auto      PASS    
3.6  Sequence gap bridged with SequenceReset-GapFill   req  auto      PASS    
3.7  Graceful Logout acknowledged                      req  auto      PASS    
3.8  Recovery after a disconnect without Logout        req  auto      PASS    
4.1  New Order Single — Market Buy                     req  auto      PASS    
4.2  New Order Single — Market Sell                    req  auto      PASS    
4.3  New Order Single — Limit Buy                      req  auto      PASS    
4.4  New Order Single — Limit Sell                     req  auto      PASS    
4.5  IOC order                                         req  auto      N/A     target orderecho-emulator: Emulator supports TimeInForce Day only
4.6  FOK order                                         req  auto      N/A     target orderecho-emulator: Emulator supports TimeInForce Day only
4.7  Day order                                         req  auto      PASS    
4.8  GTC order persists across session restart         opt  auto      N/A     target orderecho-emulator: Emulator supports TimeInForce Day only
4.9  GTX order                                         opt  auto      N/A     target orderecho-emulator: Emulator supports TimeInForce Day only
5.1  Pending New before the acknowledgement            opt  auto      N/A     target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
5.2  New acknowledgement                               req  auto      PASS    
5.3  Partial fill                                      req  assisted  PASS    
5.4  Full fill                                         req  auto      PASS    
5.5  Done For Day                                      opt  assisted  N/A     target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
5.6  Expired                                           opt  assisted  N/A     target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
5.7  ExecIDs unique across the session                 req  auto      PASS    
5.8  AvgPx and CumQty accumulate across partial fills  req  assisted  PASS    
6.1  Order Cancel Request references ClOrdID and Ori…  req  auto      PASS    
6.2  Cancel acknowledgement                            req  auto      PASS    
6.3  Cancel Reject for a fully-filled order            req  auto      PASS    
6.4  Cancel Reject — too late to cancel (order pendi…  req  assisted  N/A     target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
6.5  Cancel/Replace — price only                       req  auto      PASS    
6.6  Cancel/Replace — quantity up                      req  auto      PASS    
6.7  Cancel/Replace — quantity down                    opt  auto      PASS    
6.8  Pending Replace then Replace ack                  opt  auto      N/A     target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
7.1  Missing required tag — Session Reject             req  auto      PASS    
7.2  Invalid MsgType — reject                          req  auto      PASS    
7.3  Invalid Symbol — reject                           req  auto      PASS    
7.4  Invalid Price — reject                            req  auto      PASS    
7.5  Invalid Side — reject with RefTagID=54            req  auto      PASS    
7.6  Invalid Account — reject                          opt  auto      N/A     target orderecho-emulator: Emulator does not validate Account (1)
7.7  Duplicate ClOrdID — reject                        req  auto      PASS    
7.8  BusinessMessageReject structure                   req  auto      PASS    warning: step 3 (assert_received): last received message: 35=j seq=66 45=62 372=ZZ 380=3 58=Not supported in this build; recommended tag(s) absent: 3…
8.1  Mid-session disconnect: ResendRequest / gap fil…  req  assisted  PASS    
8.2  PossDup resend of an order is not re-executed     req  auto      PASS    
8.3  Execution Reports replayed with PossDupFlag=Y     req  auto      PASS    
8.4  ResendRequest while Execution Reports are in fl…  opt  auto      PASS    
8.5  No duplicate ExecIDs on replay                    req  auto      PASS    
8.6  Client restart with sequence reset                req  auto      PASS    
8.7  Exchange restart: reconnect, gap fill, open ord…  opt  assisted  N/A     target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
9.1  All required tests passed                         req  manual    PASS    attested pass by Cert operator: All required cases PASS or N/A in this run
9.2  Venue deviations documented                       req  manual    PASS    attested pass by Cert operator: Deviations: the N/A reasons and the 7.8 warning (no 379)
9.3  Written certification approval                    req  manual    PASS    attested pass by Cert operator: Demonstration run: approval simulated
9.4  Approval filed internally                         req  manual    PASS    attested pass by Cert operator: Demonstration run: filing simulated
9.5  Results shared with compliance and operations     opt  manual    N/A     attested na by Cert operator: Demonstration run
9.6  Production cutover scheduled                      opt  manual    N/A     attested na by Cert operator: Demonstration run

all cases     : PASS 54, N/A 14
required cases: PASS 51, N/A 3
exit code     : 0
results       : data/certs/20260929-085954.753
exit 0
```

### 3.3 Negative control: `--session strict` (the strict broker rejects every order) → exit 5
```
order-entry-fix42 — Order Entry Certification — FIX 4.2 US Equities
target orderecho-emulator, session strict (FIX.4.2), run 20260929-090046.738, agent 0.3.0 (a3)

ID   TITLE                                             REQ  MODE      STATUS   REASON
1.1  Certification environment details and documenta…  req  manual    PENDING  needs a human attestation: Confirm the cert host, port and the venue's documentation package were received.
1.2  TCP connectivity to the cert host                 req  auto      PASS     
1.3  Outbound firewall permits FIX egress              req  auto      PASS     
1.4  Inbound rules for a different response IP/port    opt  manual    PENDING  needs a human attestation: Confirm whether the venue pushes responses from a different IP/port range, and that inbound rules allow it (or N/A).
1.5  CompIDs, password and sequence reset policy col…  req  manual    PENDING  needs a human attestation: Confirm SenderCompID, TargetCompID, session password and the venue's sequence reset policy were provisioned and recorded.
1.6  TLS vs clear-text requirement confirmed           req  manual    PENDING  needs a human attestation: Confirm with the venue's onboarding team whether TLS is required (this agent speaks clear-text TCP).
1.7  UAT endpoint confirmed                            req  manual    PENDING  needs a human attestation: Confirm the configured host/port is the venue's UAT (certification) endpoint, not production.
2.1  BeginString configured                            req  auto      PASS     
2.2  SenderCompID / TargetCompID as provisioned        req  auto      PASS     
2.3  HeartBtInt configured                             req  auto      PASS     
2.4  EncryptMethod = 0                                 req  auto      PASS     
2.5  ResetOnLogon for the cert session                 opt  auto      PASS     
2.6  Clean sequence state before the cert              req  auto      PASS     
2.7  ResetSeqNumFlag=Y accepted on reconnect           req  auto      PASS     
3.1  Logon acknowledged                                req  auto      PASS     
3.2  Bidirectional heartbeats at HeartBtInt            req  auto      PASS     
3.3  TestRequest answered with matching Heartbeat      req  auto      PASS     
3.4  Exchange-initiated TestRequest answered           req  assisted  PASS     
3.5  ResendRequest replayed with PossDupFlag=Y         req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=8 11=OE-20260929-090046.738-1 37…
3.6  Sequence gap bridged with SequenceReset-GapFill   req  auto      PASS     
3.7  Graceful Logout acknowledged                      req  auto      PASS     
3.8  Recovery after a disconnect without Logout        req  auto      PASS     
4.1  New Order Single — Market Buy                     req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=16 11=OE-20260929…
4.2  New Order Single — Market Sell                    req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=17 11=OE-20260929…
4.3  New Order Single — Limit Buy                      req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=18 11=OE-20260929…
4.4  New Order Single — Limit Sell                     req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=19 11=OE-20260929…
4.5  IOC order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
4.6  FOK order                                         req  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
4.7  Day order                                         req  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW from the counterparty: 35=8 seq=20 11=OE-20260929…
4.8  GTC order persists across session restart         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
4.9  GTX order                                         opt  auto      N/A      target orderecho-emulator: Emulator supports TimeInForce Day only
5.1  Pending New before the acknowledgement            opt  auto      N/A      target orderecho-emulator: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0
5.2  New acknowledgement                               req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW ord_status=NEW 14=0 151=100 present [37 17] from the counterpart…
5.3  Partial fill                                      req  assisted  FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=22 11=OE-20260929-090046.738-8 3…
5.4  Full fill                                         req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=FILL ord_status=FILLED 14=100 151=0 from the counterparty: 35=8 seq=…
5.5  Done For Day                                      opt  assisted  N/A      target orderecho-emulator: Emulator has no Done For Day behavior and no control endpoint for it
5.6  Expired                                           opt  assisted  N/A      target orderecho-emulator: Emulator never expires orders (TimeInForce Day only, no GTD/IOC)
5.7  ExecIDs unique across the session                 req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=24 11=OE-20260929-090046.738…
5.8  AvgPx and CumQty accumulate across partial fills  req  assisted  FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=25 11=OE-20260929-090046.738-11 …
6.1  Order Cancel Request references ClOrdID and Ori…  req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=26 11=OE-20260929-090046.738-12 …
6.2  Cancel acknowledgement                            req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=27 11=OE-20260929-090046.738-13 …
6.3  Cancel Reject for a fully-filled order            req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=28 11=OE-20260929-090046.738…
6.4  Cancel Reject — too late to cancel (order pendi…  req  assisted  N/A      target orderecho-emulator: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3
6.5  Cancel/Replace — price only                       req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=29 11=OE-20260929-090046.738-15 …
6.6  Cancel/Replace — quantity up                      req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=30 11=OE-20260929-090046.738-16 …
6.7  Cancel/Replace — quantity down                    opt  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=31 11=OE-20260929-090046.738-17 …
6.8  Pending Replace then Replace ack                  opt  auto      N/A      target orderecho-emulator: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace
7.1  Missing required tag — Session Reject             req  auto      PASS     
7.2  Invalid MsgType — reject                          req  auto      PASS     
7.3  Invalid Symbol — reject                           req  auto      PASS     
7.4  Invalid Price — reject                            req  auto      PASS     
7.5  Invalid Side — reject with RefTagID=54            req  auto      PASS     
7.6  Invalid Account — reject                          opt  auto      N/A      target orderecho-emulator: Emulator does not validate Account (1)
7.7  Duplicate ClOrdID — reject                        req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=37 11=OE-20260929-090046.738-19 …
7.8  BusinessMessageReject structure                   req  auto      PASS     warning: step 3 (assert_received): last received message: 35=j seq=38 45=42 372=ZZ 380=3 58=Not supported in this build; recommended tag(s) absent: 3…
8.1  Mid-session disconnect: ResendRequest / gap fil…  req  assisted  PASS     
8.2  PossDup resend of an order is not re-executed     req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for exec_type=NEW from the counterparty: 35=8 seq=44 11=OE-20260929-090046.738-20 …
8.3  Execution Reports replayed with PossDupFlag=Y     req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=45 11=OE-20260929-090046.738…
8.4  ResendRequest while Execution Reports are in fl…  opt  auto      FAIL     step 3 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=46 11=OE-20260929-090046.738…
8.5  No duplicate ExecIDs on replay                    req  auto      FAIL     step 2 (expect): the counterparty rejected the order while waiting for ord_status=FILLED from the counterparty: 35=8 seq=47 11=OE-20260929-090046.738…
8.6  Client restart with sequence reset                req  auto      PASS     
8.7  Exchange restart: reconnect, gap fill, open ord…  opt  assisted  N/A      target orderecho-emulator: Emulator orders do not survive an engine restart and it has no restart endpoint
9.1  All required tests passed                         req  manual    PENDING  needs a human attestation: Review results.json: every required case is PASS (or N/A with the venue's agreement).
9.2  Venue deviations documented                       req  manual    PENDING  needs a human attestation: Document the venue's deviations from FIX 4.2 seen during certification (warnings and N/A reasons are a starting point).
9.3  Written certification approval                    req  manual    PENDING  needs a human attestation: Obtain the venue's written certification approval.
9.4  Approval filed internally                         req  manual    PENDING  needs a human attestation: File the approval email in the internal tracking system with date and venue name.
9.5  Results shared with compliance and operations     opt  manual    PENDING  needs a human attestation: Share the certification results with compliance and operations.
9.6  Production cutover scheduled                      opt  manual    PENDING  needs a human attestation: Schedule the production cutover date and confirm it with the venue's go-live team.

all cases     : PASS 24, FAIL 22, PENDING 11, N/A 11
required cases: PASS 23, FAIL 20, PENDING 8, N/A 3
exit code     : 5
results       : data/certs/20260929-090046.738
exit 5
```

### 3.4 One case's evidence folder: 5.8 (assisted; AvgPx across two partial fills)

The run is `data/certs/20260929-085954.753`, the attested one. The folder listing, the case's `fix.log` and its `results.json` entry are below. Note `6=10.4000` = (30×10.00 + 20×11.00) / 50. The last F/ER pair is the runner's cleanup cancel.
```
$ ls -la data/certs/20260929-085954.753 | head
total 312
drwxr-xr-x  50 dgavin  staff    1600 Sep 29 05:00 .
drwxr-xr-x   5 dgavin  staff     160 Sep 29 05:01 ..
drwxr-xr-x   4 dgavin  staff     128 Sep 29 05:00 1.2
drwxr-xr-x   4 dgavin  staff     128 Sep 29 05:00 1.3
drwxr-xr-x   4 dgavin  staff     128 Sep 29 05:00 2.1
drwxr-xr-x   4 dgavin  staff     128 Sep 29 05:00 2.2
drwxr-xr-x   4 dgavin  staff     128 Sep 29 05:00 2.3
...
$ ls -la data/certs/20260929-085954.753/5.8
total 40
drwxr-xr-x   4 dgavin  staff    128 Sep 29 05:00 .
drwxr-xr-x  50 dgavin  staff   1600 Sep 29 05:00 ..
-rw-r--r--   1 dgavin  staff  13070 Sep 29 05:00 evidence.jsonl
-rw-r--r--   1 dgavin  staff   1748 Sep 29 05:00 fix.log

$ cat data/certs/20260929-085954.753/5.8/fix.log
20260929-09:00:40.063 OUT  seq=37   35=D  8=FIX.4.2|9=151|35=D|49=AGENT|56=ORDERECHO|34=37|52=20260929-09:00:40.063|11=OE-20260929-085954.753-18|21=1|55=ZWZZT|54=1|60=20260929-09:00:40.058|38=100|40=2|44=1.00|10=131|
20260929-09:00:40.067 IN   seq=38   35=8  8=FIX.4.2|9=245|35=8|49=ORDERECHO|56=AGENT|34=38|52=20260929-09:00:40.066|37=O-20260929-075504-42|11=OE-20260929-085954.753-18|17=E-20260929-075504-88|20=0|150=0|39=0|55=ZWZZT|54=1|38=100|40=2|44=1.00|32=0|31=0.00|151=100|14=0|6=0.0000|60=20260929-09:00:40.065|10=195|
20260929-09:00:40.089 IN   seq=39   35=8  8=FIX.4.2|9=248|35=8|49=ORDERECHO|56=AGENT|34=39|52=20260929-09:00:40.089|37=O-20260929-075504-42|11=OE-20260929-085954.753-18|17=E-20260929-075504-89|20=0|150=1|39=1|55=ZWZZT|54=1|38=100|40=2|44=1.00|32=30|31=10.00|151=70|14=30|6=10.0000|60=20260929-09:00:40.087|10=113|
20260929-09:00:40.096 IN   seq=40   35=8  8=FIX.4.2|9=248|35=8|49=ORDERECHO|56=AGENT|34=40|52=20260929-09:00:40.096|37=O-20260929-075504-42|11=OE-20260929-085954.753-18|17=E-20260929-075504-90|20=0|150=1|39=1|55=ZWZZT|54=1|38=100|40=2|44=1.00|32=20|31=11.00|151=50|14=50|6=10.4000|60=20260929-09:00:40.095|10=098|
20260929-09:00:40.104 OUT  seq=38   35=F  8=FIX.4.2|9=186|35=F|49=AGENT|56=ORDERECHO|34=38|52=20260929-09:00:40.104|41=OE-20260929-085954.753-18|11=OE-20260929-085954.753-19|37=O-20260929-075504-42|55=ZWZZT|54=1|60=20260929-09:00:40.101|38=100|10=253|
20260929-09:00:40.107 IN   seq=41   35=8  8=FIX.4.2|9=274|35=8|49=ORDERECHO|56=AGENT|34=41|52=20260929-09:00:40.106|37=O-20260929-075504-42|11=OE-20260929-085954.753-19|41=OE-20260929-085954.753-18|17=E-20260929-075504-91|20=0|150=4|39=4|55=ZWZZT|54=1|38=100|40=2|44=1.00|32=0|31=0.00|151=0|14=50|6=10.4000|60=20260929-09:00:40.106|10=147|

$ python3 -m json.tool: results.json case 5.8
{
  "id": "5.8",
  "section": "LCY",
  "title": "AvgPx and CumQty accumulate across partial fills",
  "task": "Verify AvgPx (6) and CumQty (14) accumulate correctly across multiple partial fills",
  "required": true,
  "level": "intermediate",
  "mode": "assisted",
  "status": "PASS",
  "reason": "",
  "steps": [
    {
      "type": "send",
      "status": "PASS",
      "detail": "D 11=OE-20260929-085954.753-18 buy 100 ZWZZT lmt 1.00 ",
      "ts": "2026-09-29T09:00:40.063Z"
    },
    {
      "type": "expect",
      "status": "PASS",
      "detail": "matched 35=8 seq=38 11=OE-20260929-085954.753-18 37=O-20260929-075504-42 150=0 39=0 32=0 14=0 151=100 6=0.0000",
      "ts": "2026-09-29T09:00:40.085Z"
    },
    {
      "type": "control",
      "status": "PASS",
      "detail": "the counterparty fills 30 shares at 10.00 (POST /orders/O-20260929-075504-42/fill {\"price\":\"10.00\",\"qty\":30} -> 200)",
      "ts": "2026-09-29T09:00:40.093Z"
    },
    {
      "type": "expect",
      "status": "PASS",
      "detail": "matched 35=8 seq=39 11=OE-20260929-085954.753-18 37=O-20260929-075504-42 150=1 39=1 32=30 14=30 151=70 6=10.0000",
      "ts": "2026-09-29T09:00:40.093Z"
    },
    {
      "type": "control",
      "status": "PASS",
      "detail": "the counterparty fills 20 more shares at 11.00 (POST /orders/O-20260929-075504-42/fill {\"price\":\"11.00\",\"qty\":20} -> 200)",
      "ts": "2026-09-29T09:00:40.101Z"
    },
    {
      "type": "expect",
      "status": "PASS",
      "detail": "matched 35=8 seq=40 11=OE-20260929-085954.753-18 37=O-20260929-075504-42 150=1 39=1 32=20 14=50 151=50 6=10.4000",
      "ts": "2026-09-29T09:00:40.101Z"
    },
    {
      "type": "checks",
      "status": "PASS",
      "detail": "11 checks: o1=PASS",
      "ts": "2026-09-29T09:00:40.101Z"
    },
    {
      "type": "cleanup",
      "status": "PASS",
      "detail": "canceled working order(s) OE-20260929-085954.753-18",
      "ts": "2026-09-29T09:00:40.126Z"
    }
  ],
  "orders": [
    "OE-20260929-085954.753-18"
  ],
  "checks": [
    {
      "ref": "o1",
      "cl_ord_id": "OE-20260929-085954.753-18",
      "verdict": "PASS",
      "checks": [
        {
          "name": "cum_qty_monotonic",
          "status": "PASS",
          "explanation": "CumQty rose to 50 without ever falling",
          "rule": "CumQty never decreases"
        },
        {
          "name": "working_quantities",
          "status": "PASS",
          "explanation": "3 working report(s) balanced",
          "rule": "working states: CumQty + LeavesQty == OrderQty"
        },
        {
          "name": "terminal_quantities",
          "status": "PASS",
          "explanation": "no terminal reports to check",
          "rule": "terminal states: LeavesQty == 0, and 39=2 implies CumQty == OrderQty"
        },
        {
          "name": "fill_quantities_sum",
          "status": "PASS",
          "explanation": "2 fill(s) totalling 50 match CumQty",
          "rule": "sum of LastQty over fills == final CumQty"
        },
        {
          "name": "avg_px",
          "status": "PASS",
          "explanation": "AvgPx 10.4000 matches the fills to within 0.0001",
          "rule": "AvgPx == sum(LastQty x LastPx) / CumQty, within 0.0001"
        },
        {
          "name": "exec_ids_unique",
          "status": "PASS",
          "explanation": "3 ExecID(s), all distinct",
          "rule": "ExecIDs are unique within the chain"
        },
        {
          "name": "order_id_constant",
          "status": "PASS",
          "explanation": "OrderID O-20260929-075504-42 throughout",
          "rule": "OrderID is constant across the chain"
        },
        {
          "name": "nothing_after_terminal",
          "status": "PASS",
          "explanation": "the order never reached a terminal state",
          "rule": "no state change after a terminal report (replays excepted)"
        },
        {
          "name": "version_rules",
          "status": "PASS",
          "explanation": "every report matches its version's conventions",
          "rule": "ExecType and ExecTransType match the message's FIX version"
        },
        {
          "name": "requests_answered",
          "status": "PASS",
          "explanation": "all 1 request(s) answered",
          "rule": "each D/F/G is answered by an ER, a cancel reject or a Reject"
        },
        {
          "name": "framing_intact",
          "status": "PASS",
          "explanation": "all 4 message(s) correctly framed",
          "rule": "every message in the chain is correctly framed (9 and 10 agree)"
        }
      ]
    }
  ],
  "start": "2026-09-29T09:00:40.058Z",
  "end": "2026-09-29T09:00:40.126Z"
}
```

## 4. Mapping: every checklist row

Mode counts: auto 49, assisted 8, manual 11. "Emulator target" says how the row runs against `certs/targets/emulator.yaml`. The last column gives the real-run statuses from §3.1 / §3.2 / §3.3.

| Row | Section | Req | Mode | How it is verified | Emulator target | Real run (no attest / attested / strict) |
|---|---|---|---|---|---|---|
| 1.1 | ENV | req | manual | Human confirms the cert host/port and documentation were received. | attestation | PENDING / PASS / PENDING |
| 1.2 | ENV | req | auto | Asserts a Logon was sent and answered on the connection, then a TestRequest round trip. | runs | PASS / PASS / PASS |
| 1.3 | ENV | req | auto | An established, answered session proves egress to the FIX port; TestRequest round trip. | runs | PASS / PASS / PASS |
| 1.4 | ENV | opt | manual | Human confirms response IP/port ranges (or N/A). | attestation | PENDING / N/A / PENDING |
| 1.5 | ENV | req | manual | Human confirms CompIDs, password and reset policy were provisioned. | attestation | PENDING / PASS / PENDING |
| 1.6 | ENV | req | manual | Human confirms the TLS vs clear-text requirement with the venue. | attestation | PENDING / PASS / PENDING |
| 1.7 | ENV | req | manual | Human confirms the endpoint is UAT. | attestation | PENDING / PASS / PENDING |
| 2.1 | SES | req | auto | Our Logon and the reply both carry 8={{fix_version}}. | runs | PASS / PASS / PASS |
| 2.2 | SES | req | auto | Our Logon has 49/56 as configured; the reply mirrors them exactly. | runs | PASS / PASS / PASS |
| 2.3 | SES | req | auto | Our Logon and the reply both carry 108={{heartbeat_sec}}. | runs | PASS / PASS / PASS |
| 2.4 | SES | req | auto | Our Logon and the reply both carry 98=0. | runs | PASS / PASS / PASS |
| 2.5 | SES | opt | auto | The run's first Logon (and its reply) carry 141=Y. | runs | PASS / PASS / PASS |
| 2.6 | SES | req | auto | Clean start: the first Logon is 34=1 with 141=Y and the reply is 34=1. | runs | PASS / PASS / PASS |
| 2.7 | SES | req | auto | Logout, reconnect with 141=Y: we send 34=1/141=Y, the reply is 34=1/141=Y, TestRequest round trip. | runs | PASS / PASS / PASS |
| 3.1 | MSG | req | auto | Logout, reconnect: the counterparty's Logon reply arrives (with 98 and 108). | runs | PASS / PASS / PASS |
| 3.2 | MSG | req | auto | Waits HeartBtInt+20%+5s; an unsolicited Heartbeat arrived from each side. | runs | PASS / PASS / PASS |
| 3.3 | MSG | req | auto | TestRequest with our TestReqID answered by a Heartbeat with the same 112. | runs | PASS / PASS / PASS |
| 3.4 | MSG | req | assisted | Control API makes the counterparty send 35=1; we answer with 35=0 carrying 112. | control API | PASS / PASS / PASS |
| 3.5 | MSG | req | auto | Order acked, then our ResendRequest from the case's first inbound seq: an ER comes back with 43=Y and 122. | runs | PASS / PASS / FAIL |
| 3.6 | MSG | req | auto | We skip 3 outbound seqs; the counterparty sends 35=2; we answer with 35=4 123=Y; TestRequest round trip. | runs | PASS / PASS / PASS |
| 3.7 | MSG | req | auto | Clean Logout exchange; their 35=5 received. | runs | PASS / PASS / PASS |
| 3.8 | MSG | req | auto | Skip 2 seqs, drop the socket, reconnect without reset: they send 35=2, we gap-fill (35=4 123=Y), TestRequest round trip. | runs | PASS / PASS / PASS |
| 4.1 | ORD | req | auto | Market buy: our D has 40=1 54=1 and no 44; NEW ack; fill optional; 11 checks. | runs | PASS / PASS / FAIL |
| 4.2 | ORD | req | auto | Market sell: 40=1 54=2, no 44; NEW ack; 11 checks. | runs | PASS / PASS / FAIL |
| 4.3 | ORD | req | auto | Limit buy far below the market: 40=2 54=1 44=limit_buy; NEW ack; 11 checks. | runs | PASS / PASS / FAIL |
| 4.4 | ORD | req | auto | Limit sell far above the market: 40=2 54=2 44=limit_sell; NEW ack; 11 checks. | runs | PASS / PASS / FAIL |
| 4.5 | ORD | req | auto | IOC limit (59=3): must end FILLED/CANCELED/EXPIRED, never rest; 11 checks. | N/A: Emulator supports TimeInForce Day only | N/A / N/A / N/A |
| 4.6 | ORD | req | auto | FOK limit (59=4): terminal FILLED/CANCELED/EXPIRED and no partial fill; 11 checks. | N/A: Emulator supports TimeInForce Day only | N/A / N/A / N/A |
| 4.7 | ORD | req | auto | Day limit (59=0): NEW ack; 11 checks. | runs | PASS / PASS / FAIL |
| 4.8 | ORD | opt | auto | GTC limit (59=1): NEW, logout, reconnect, cancel accepted (still live); 11 checks. | N/A: Emulator supports TimeInForce Day only | N/A / N/A / N/A |
| 4.9 | ORD | opt | auto | GTX limit (59=5): NEW ack; 11 checks. | N/A: Emulator supports TimeInForce Day only | N/A / N/A / N/A |
| 5.1 | LCY | opt | auto | Limit order: Pending New (150=A 39=A) then NEW; 11 checks. | N/A: Emulator never sends Pending New (150=A); it acknowledges directly with 150=0 | N/A / N/A / N/A |
| 5.2 | LCY | req | auto | NEW ack with 14=0, 151=qty, 37 and 17 present; 11 checks. | runs | PASS / PASS / FAIL |
| 5.3 | LCY | req | assisted | Resting order; control API fills 40: ER 150=1 39=1 with 32=40 14=40 151=60 and 31; 11 checks. | control API | PASS / PASS / FAIL |
| 5.4 | LCY | req | auto | Market order: fill with 151=0 and 14=OrderQty (4.2 150=2; 4.4 150=F 39=2); 11 checks. | runs | PASS / PASS / FAIL |
| 5.5 | LCY | opt | assisted | Counterparty ends the day: expect 150=3 39=3. | N/A: Emulator has no Done For Day behavior and no control endpoint for it | N/A / N/A / N/A |
| 5.6 | LCY | opt | assisted | Counterparty expires an IOC: expect 150=C 39=C. | N/A: Emulator never expires orders (TimeInForce Day only, no GTD/IOC) | N/A / N/A / N/A |
| 5.7 | LCY | req | auto | Two filled orders; every ExecID among the session's non-PossDup ERs is distinct; 11 checks. | runs | PASS / PASS / FAIL |
| 5.8 | LCY | req | assisted | Resting order; control API fills 30@10.00 then 20@11.00: CumQty 30 then 50; the 11 checks verify AvgPx = 10.4 within 0.0001. | control API | PASS / PASS / FAIL |
| 6.1 | CXL | req | auto | Resting order, F: our F carries 41 = the order's ClOrdID plus 11/55/54/38; cancel ack; 11 checks. | runs | PASS / PASS / FAIL |
| 6.2 | CXL | req | auto | Cancel ack 35=8 150=4 39=4 with 151=0 and 41 = the original ClOrdID; 11 checks. | runs | PASS / PASS / FAIL |
| 6.3 | CXL | req | auto | Filled order, forced F: 35=9 with 102=0 and 434=1; 11 checks. | runs | PASS / PASS / FAIL |
| 6.4 | CXL | req | assisted | Counterparty holds the order pending fill, F: 35=9 with 102 in {0,1} and 434=1. | N/A: Emulator cannot hold an order in a pending-fill state; its too-late cancel reject (102=0, closed order) is covered by 6.3 | N/A / N/A / N/A |
| 6.5 | CXL | req | auto | Resting order, G with a new price only: our G has the same 38/54, new 44, 41 = original; REPLACED; 11 checks. | runs | PASS / PASS / FAIL |
| 6.6 | CXL | req | auto | G with the quantity up: REPLACED with 38=qty_up and 151=qty_up; 11 checks. | runs | PASS / PASS / FAIL |
| 6.7 | CXL | opt | auto | G with the quantity down: REPLACED with 38=qty_down and 151=qty_down; 11 checks. | runs | PASS / PASS / FAIL |
| 6.8 | CXL | opt | auto | G: Pending Replace (150=E 39=E) then REPLACED; 11 checks. | N/A: orderecho_multi.yaml runs the emulator with send_pending_acks: false, so it sends no Pending Replace | N/A / N/A / N/A |
| 7.1 | REJ | req | auto | Raw D without 55: 35=3 with 373=1 and 371=55. | runs | PASS / PASS / PASS |
| 7.2 | REJ | req | auto | Raw 35=ZZ: 35=3 373=11, or 35=j 380=3 372=ZZ. | runs | PASS / PASS / PASS |
| 7.3 | REJ | req | auto | D on an invalid symbol: ER 150=8 39=8, or 35=j; 11 checks. | runs | PASS / PASS / PASS |
| 7.4 | REJ | req | auto | Raw limit D with 44=-1.00: ER reject, 35=3 or 35=j; 11 checks. | runs | PASS / PASS / PASS |
| 7.5 | REJ | req | auto | Raw D with 54=Z: 35=3 with 371=54. | runs | PASS / PASS / PASS |
| 7.6 | REJ | opt | auto | D with an invalid Account (1): ER reject, 35=j, or 35=3 371=1. | N/A: Emulator does not validate Account (1) | N/A / N/A / N/A |
| 7.7 | REJ | req | auto | Resting order, then a raw D reusing its ClOrdID: ER 150=8 103=6 (or 35=j); the original order's checks still PASS (A3 3.2). | runs | PASS / PASS / FAIL |
| 7.8 | REJ | req | auto | Raw 35=ZZ: 35=j with 45 = our seq, 372=ZZ, 380 and 58; 379 recommended (warning if absent). | runs | PASS / PASS / PASS |
| 8.1 | RCV | req | assisted | Control API skips 3 counterparty seqs, we drop and reconnect: we send a closed-range 35=2, they gap-fill (35=4 123=Y), TestRequest round trip. | control API | PASS / PASS / PASS |
| 8.2 | RCV | req | auto | Resting order; our D re-sent on its original seq with 43=Y/122: no new ER within 3s; TestRequest round trip; 11 checks. | runs | PASS / PASS / FAIL |
| 8.3 | RCV | req | auto | Filled order; our ResendRequest: its ER replayed with 43=Y, 122 and the same 17; 11 checks. | runs | PASS / PASS / FAIL |
| 8.4 | RCV | opt | auto | Market order with a ResendRequest sent before the fill: the fill arrives, the session stays in sequence; 11 checks. | runs | PASS / PASS / FAIL |
| 8.5 | RCV | req | auto | Filled order, ResendRequest, PossDup replay received: session ExecIDs unique (replays excluded); 11 checks. | runs | PASS / PASS / FAIL |
| 8.6 | RCV | req | auto | Logout, reconnect with a reset: we send 34=1 141=Y, the reply has 141=Y; TestRequest round trip. | runs | PASS / PASS / PASS |
| 8.7 | RCV | opt | assisted | Counterparty restarts; we reconnect and cancel the resting order successfully. | N/A: Emulator orders do not survive an engine restart and it has no restart endpoint | N/A / N/A / N/A |
| 9.1 | SGN | req | manual | Human confirms every required case is PASS (from results.json). | attestation | PENDING / PASS / PENDING |
| 9.2 | SGN | req | manual | Human documents the venue's deviations (N/A reasons and warnings are the starting point). | attestation | PENDING / PASS / PENDING |
| 9.3 | SGN | req | manual | Human obtains the venue's written approval. | attestation | PENDING / PASS / PENDING |
| 9.4 | SGN | req | manual | Human files the approval internally. | attestation | PENDING / PASS / PENDING |
| 9.5 | SGN | opt | manual | Human shares the results with compliance and operations. | attestation | PENDING / N/A / PENDING |
| 9.6 | SGN | opt | manual | Human schedules the production cutover. | attestation | PENDING / N/A / PENDING |

## 5. Decisions I made (conservative, logged)

1. **The suite's format and its extensions to the spec's step list.**
   - `expect.from: us` checks our own messages: our Heartbeats, gap fills and ResendRequests.
   - `expect.none` asserts that nothing arrives within the window (8.2, 4.6).
   - `expect.any_of` lists alternative acceptable answers (7.2–7.4, 7.6, 7.7).
   - `assert.which: first|last` and `assert.recommended` (missing gives a warning).
   - `checks: session_exec_ids` (5.7, 8.5).
   - `session: resend_order` re-sends our stored D on its original seq with 43=Y (8.2).
   - `session: testreq` optionally does not require an answer (3.6).
   - `send.allow_terminal` cancels an order already reported filled (6.3).

   These are the smallest additions that let the rows be verified by code.
2. **Built-in variables:** `fix_version`, `sender_comp_id`, `target_comp_id`, `session`, `heartbeat_sec`, `hb_window` (= HeartBtInt + 20% + 5 s), `uid` (a fresh ClOrdID per use), `transact_time`, `case_first_in_seq`, `last_testreq_id`, and `order.<ref>.{root,cl_ord_id,order_id,seq,state}`. Precedence: suite < target < `--var` < built-ins.
3. **YAML scalars are kept verbatim**, so `limit_buy: 1.00` is sent as `44=1.00`. Control-API bodies use YAML's own typing: `qty: 40` goes as a number, `price: "10.00"` as a string.
4. **Mode assignment.**
   - Manual: the ENV rows except 1.2/1.3, plus all SGN rows.
   - Auto: 1.2 and 1.3, because an answered Logon on the connection proves connectivity and egress. Also 2.6, a clean start verified as 34=1/141=Y on the first Logon (the storage path itself is our own).
   - Assisted: rows needing the counterparty to act (3.4, 5.3, 5.5, 5.6, 5.8, 6.4, 8.1, 8.7).
   - 5.3 and 5.8 are assisted, not auto: a partial fill at two known prices can't be driven with any venue alone; the emulator does it through its control API.
   - A control step without an endpoint (5.5, 5.6, 6.4, 8.7) needs a human (or an attestation) even with a control API, and the emulator target marks those N/A.
5. **Emulator N/A list (11 rows).** Each reason is taken from the emulator's own behaviour:
   - TIF other than Day is unsupported: 4.5, 4.6, 4.8, 4.9.
   - It never sends Pending New: 5.1.
   - It has no Done For Day or Expired: 5.5, 5.6.
   - It cannot put an order in a pending-fill state: 6.4.
   - `orderecho_multi.yaml` sets `send_pending_acks: false`: 6.8.
   - It does not validate Account: 7.6.
   - Its orders don't survive a restart and it has no restart endpoint: 8.7.
6. **Emulator target symbols** follow `orderecho_multi.yaml`'s rules: AAPL fills; ZWZZT rests (and serves as the "limit" and "hold" symbol); ZVZZT is the invalid symbol (103=1 "Unknown symbol"). The suite's defaults for a real venue are AAPL with limits far from the market (1.00 / 99999.00).
7. **Order scoping.** An `expect` for 8/9/3/j is scoped to the case's most recent order unless `order:` names another one: 8/9 match by 11/41, 3/j by RefSeqNum. Expect steps consume messages in order (one cursor per direction). `session: testreq` only *peeks* at its Heartbeat, so a later expect can see it too.
8. **Fail fast on a reject.** If the order the step waits on is rejected (39=8) and the step can't accept a reject, it FAILs at once with the reject's text. The negative control then takes 35 s instead of minutes, and its reasons are clearer.
9. **Every case ends with a best-effort cleanup:** it cancels its own still-working orders, for isolation (shown as a `cleanup` step; it never changes the result). Each case has its own refs and ClOrdIDs, with `cert case start` / `cert case end` evidence markers, and its `fix.log` and `evidence.jsonl` are byte ranges of the run's files between those markers.
10. **Every cert run starts with a sequence reset** (141=Y); rows 2.5 and 2.6 verify it. The runner does its own reconnects; config `reconnect` is ignored during a run. FIX/engine console echo is off unless `--verbose`, since the progress lines are the console output.
11. **Statuses.**
    - ERROR is kept for infrastructure: a session drop, a control-API failure, an unusable step at run time.
    - A case not run because of `--stop-on-fail` is `NOT_RUN`, a seventh status the spec did not list.
    - Assisted with a non-executable control step: an attestation decides the case, and without one it is BLOCKED. Assisted with an executable control step: the run decides, and any attestation is recorded but not used.
    - N/A overrides everything, with an attestation noted as ignored.
12. **Exit-code precedence:** session never established (1) > session died and could not be re-established (3) > any ERROR (8) > required FAIL (5) > required BLOCKED/PENDING (7) > logout timeout at the end (4) > 0. Optional cases never change the exit code.
13. **Suite and session FIX versions must match.** `cert run` refuses a mismatch with exit 2; there is no silent override.
14. **§3.1 closed range.** A gap requests `7=expected 16=<revealing seq − 1>`. Held messages are processed in order once the range is filled, and held copies a fill skipped over are dropped (replacing A2's rule). If held messages remain behind a further gap once the range is complete, a new ResendRequest goes out. A Logon reply that revealed a gap has its seq consumed once the fill arrives. The A1/A2 tests that encoded the old rule were updated to the new one: A1 interop scenario 3, the session gap tests, and A2's "covered by a gap fill" test, which now asserts a drop.
15. **§3.2 chain split.** The established OrderID is the first one a report carries. A later request reusing a ClOrdID, with its 39=8 answer on a different OrderID (and any 35=3/35=j answering it), becomes its own chain. `timeline --order-id <dup's OrderID>` shows the split-off chain. A2 scenario 9's four parity cases are now documented divergences (Go PASS, Python FAIL on `order_id_constant`), asserted to diverge exactly that way.
16. **§3.3 grace** applies only to the order manager's live checks, at `answer_grace_sec` (default 5). Offline `timeline`, the parity harness and the runner's `checks: timeline` step use the ungraced checks.
17. **§3.4 run IDs** are `YYYYMMDD-HHMMSS.mmm`. The dot is filesystem-safe and appears in ClOrdIDs (`OE-20260929-085954.753-18`) and in evidence file names.

## 6. Questions for you

1. **Row 6.4 contradicts FIX.** The checklist asks for "Too Late to Cancel (CxlRejReason=1)", but in FIX 4.2 102=0 means Too late to cancel and 1 means Unknown order. The case accepts either. Which do you want enforced?
2. **Row 7.2 asks for a Session Reject with RefTagID for an invalid MsgType.** Many engines, the emulator included, answer with BusinessMessageReject 380=3 instead. The case accepts 35=3 373=11 or 35=j 380=3. Should it be strict (35=3 only)? That would FAIL on the emulator.
3. **Row 7.8 lists BusinessRejectRefID (379)**, which FIX makes conditional. The emulator omits it, so the case PASSes with a warning. Should a missing 379 FAIL?
4. **Manual vs auto for 1.2, 1.3 and 2.6.** I made them auto because the session itself proves them. Do you want them manual (human-attested) instead?
5. **Generic-venue defaults.** The suite assumes that a limit at 1.00 / 99999.00 rests on AAPL, and that a market order fills. Do you want target files per venue to be required rather than relying on these defaults?
6. **Your emulator.** Twice now it has been running on the standard ports during my real run. Would you rather I use a copy on other ports next time, to keep my traffic out of your emulator's data?
