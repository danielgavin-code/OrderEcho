# REPORT_A2 — OrderEcho Go agent, Cook A2

A2 is built end to end. The agent now sends orders (D/F/G), keeps a shadow state for each one, and runs the 11 timeline checks, ported to Go, after every report. `make test`, `make lint` and `make interop` all pass. The interop and parity suites really ran against the real emulator: **24/24 interop tests PASS, 0 skipped**. **85/85 parity cases agree** between Go and the emulator's own Python checks. The 3 places where I made Go deliberately differ from Python are pinned by a test and listed in §6.

Version is `0.2.0` / build `a2`.

**Please read §3.1.** You had the emulator running on the standard ports when I did the real run, so I used your emulator rather than starting my own.

## 1. Files created / changed

New:
```
internal/checks/decimal.go       exact decimals (math/big), Python Decimal/int parsing rules
internal/checks/message.go       Message, split_fields, verify_framing, find_messages (Go port)
internal/checks/parse.go         parser: OrderEcho FIX log lines, evidence JSONL, generic raw FIX; merge_chronologically
internal/checks/chain.go         chain builder (41 both ways, shared 37, 35=3/35=j by RefSeqNum), replays, verdict
internal/checks/checks.go        the 11 checks, same names / statuses / explanations
internal/checks/checks_test.go   port of tests/test_Timeline.py (+ parser, decimal, divergence tests)
internal/order/order.go          PURE order manager: ClOrdIDs, D/F/G, shadow state, live checks, snapshots
internal/order/order_test.go     goldens, field layout, validation, transitions, decimal expectations
internal/agent/agent.go          assembles config -> logs/evidence/stores/session/order manager/transport; exit codes
internal/agent/agent_test.go     exit-code mapping
cmd/orderecho/conn.go            shared connect plumbing (--session/--fix-version/--reset/--reconnect), connect cmd
cmd/orderecho/ordercmd.go        order cmd, timeline printer
cmd/orderecho/repl.go            session REPL (interactive and pipe-friendly)
cmd/orderecho/timeline.go        offline timeline cmd (--clordid / --order-id / --json)
internal/interop/orders_test.go          A2 scenarios 1-11
internal/interop/parity_harness_test.go  Python parity runner (inline script importing orderecho_LogParse/Timeline)
internal/interop/parity_test.go          tampered variants + known divergences
internal/interop/cli_test.go             CLI exit codes 0/1/2/5/6, --fix-version, piped session, timeline exits
testdata/golden/FIX4{2,4}_{D,F,G}_{handlinst,nohandlinst}.txt   12 golden messages (captured once)
REPORT_A2.md
```
Changed:
```
internal/version/version.go      0.2.0 / a2
internal/fix/session/session.go  out-of-order queue (3.1), heartbeat_mismatch (3.2), Logout reply to refusal (3.5),
                                 App hooks (inbound app msgs, session rejects, outbound app sends), SendApp,
                                 SendResendRequest, Evidence.Order
internal/fix/session/session_test.go  A1 tests updated where A2 changes behaviour (3.2, 3.5); queue/mismatch/SendApp tests
internal/fix/profile/profile.go  D/F/G rendering per version, ExecType/OrdStatus names
internal/fix/transport/transport.go   evidence "order" field, Exec / Locked
internal/evidence/evidence.go    EventWithOrder
internal/config/config.go(+test) heartbeat_mismatch, include_handl_inst, clordid_prefix, account; WithFixVersion
config/orderecho.yaml            new keys shown at their defaults
cmd/orderecho/main.go            new commands; --help documents all exit codes
internal/interop/harness_test.go emulator config now mirrors orderecho_multi.yaml rules/bands with static pricing;
                                 agents built through internal/agent; parity summary printed after the run
```
The empty `EOF` file from A1 is gone; I did not remove it.

## 2. `make test`, `make lint`, `make interop`

### `make test` (121 unit tests)
```
go test -count=1 ./...
?   	github.com/danielgavin-code/OrderEcho/cmd/orderecho	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/agent	1.268s
ok  	github.com/danielgavin-code/OrderEcho/internal/checks	0.370s
?   	github.com/danielgavin-code/OrderEcho/internal/clock	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/config	0.958s
ok  	github.com/danielgavin-code/OrderEcho/internal/evidence	1.565s
ok  	github.com/danielgavin-code/OrderEcho/internal/fix/codec	2.024s
?   	github.com/danielgavin-code/OrderEcho/internal/fix/profile	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/fix/session	0.675s
?   	github.com/danielgavin-code/OrderEcho/internal/fix/transport	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/logs	1.779s
ok  	github.com/danielgavin-code/OrderEcho/internal/order	1.782s
ok  	github.com/danielgavin-code/OrderEcho/internal/store	2.345s
?   	github.com/danielgavin-code/OrderEcho/internal/version	[no test files]
```
Per package: agent 1 · checks 31 · config 4 · evidence 1 · codec 12 · session 49 · logs 5 · order 14 · store 4 = **121 PASS, 0 FAIL, 0 SKIP**. These cover:
- the D/F/G goldens for 4.2 and 4.4, with and without HandlInst/Account;
- order-manager transitions and the exact-decimal expectations;
- every check with a passing and a failing hand-made chain;
- the out-of-order queue: hold, release after a fill, in-order release, dropping an original after its replay, a held message covered by a gap fill, overflow at 1000, and the drop on disconnect;
- the HeartBtInt mismatch warn/refuse modes;
- the exit-code mapping;
- the Logout reply to a refused Logon.

### `make lint`
```
go vet ./...
go vet -tags interop ./...
```

### `make interop`

Result: **24 tests, 24 PASS, 0 SKIP**, in 117 s. That is 11 A1 scenarios (still passing), 11 A2 scenarios, 2 CLI tests, 1 tampered-parity test and 1 divergence test.
```
--- PASS: TestCLIOrderExitCodes (21.15s)
--- PASS: TestCLISessionPipedAndTimeline (16.83s)
--- PASS: TestScenario1LogonTestRequestLogout (20.57s)
--- PASS: TestScenario2EmulatorTestRequest (17.21s)
--- PASS: TestScenario3EmulatorSeqGap (1.09s)
--- PASS: TestScenario4AgentSkipOutboundSeq (1.08s)
--- PASS: TestScenario5ReconnectWithoutReset (2.37s)
--- PASS: TestScenario6WrongVersion (1.59s)
--- PASS: TestScenario7UnknownCompIDs (1.10s)
--- PASS: TestScenario8ViewerReadsAgentLogs (2.14s)
--- PASS: TestCtrlCLogsOutCleanly (1.46s)
--- PASS: TestA2Scenario01FullFill (5.43s)
--- PASS: TestA2Scenario02Partials (3.60s)
--- PASS: TestA2Scenario03OddLots (3.60s)
--- PASS: TestA2Scenario04HoldReplaceCancel (2.42s)
--- PASS: TestA2Scenario05Rejects (1.52s)
--- PASS: TestA2Scenario06UnsolicitedCancel (1.70s)
--- PASS: TestA2Scenario07ManualFills (1.08s)
--- PASS: TestA2Scenario08UnknownTagTolerated (1.57s)
--- PASS: TestA2Scenario09DuplicateClOrdID (1.17s)
--- PASS: TestA2Scenario10TestRequestBehindGap (1.81s)
--- PASS: TestA2Scenario11CancelUnknown (1.26s)
--- PASS: TestParityTampered (3.62s)
--- PASS: TestParityKnownDivergences (0.06s)
PASS
ok  	github.com/danielgavin-code/OrderEcho/internal/interop	117.044s
```
Parity cases, as the run printed them:
```
PARITY SUMMARY: 85 case(s) compared, 85 agree, 0 disagree
  AGREE    CLI session / agent FIX log / OE-20260929-074653-1                     go=PASS py=PASS [all PASS]
  AGREE    CLI session / emulator FIX log / OE-20260929-074653-1                  go=PASS py=PASS [all PASS]
  AGREE    CLI session / agent FIX log / OE-20260929-074653-2                     go=PASS py=PASS [all PASS]
  AGREE    CLI session / emulator FIX log / OE-20260929-074653-2                  go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent FIX log / IT-20260929-074745-emu428-1             go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent evidence / IT-20260929-074745-emu428-1            go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator FIX log / IT-20260929-074745-emu428-1          go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator evidence / IT-20260929-074745-emu428-1         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent FIX log / IT-20260929-074745-emu428-2             go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent evidence / IT-20260929-074745-emu428-2            go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator FIX log / IT-20260929-074745-emu428-2          go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator evidence / IT-20260929-074745-emu428-2         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent FIX log / IT-20260929-074747-emu449-1             go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent evidence / IT-20260929-074747-emu449-1            go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator FIX log / IT-20260929-074747-emu449-1          go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator evidence / IT-20260929-074747-emu449-1         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent FIX log / IT-20260929-074747-emu449-2             go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent evidence / IT-20260929-074747-emu449-2            go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator FIX log / IT-20260929-074747-emu449-2          go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator evidence / IT-20260929-074747-emu449-2         go=PASS py=PASS [all PASS]
  AGREE    A2-2 / agent FIX log / IT-20260929-074750-emu4210-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-2 / agent evidence / IT-20260929-074750-emu4210-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-2 / emulator FIX log / IT-20260929-074750-emu4210-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-2 / emulator evidence / IT-20260929-074750-emu4210-1                go=PASS py=PASS [all PASS]
  AGREE    A2-3 / agent FIX log / IT-20260929-074754-emu4411-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-3 / agent evidence / IT-20260929-074754-emu4411-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-3 / emulator FIX log / IT-20260929-074754-emu4411-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-3 / emulator evidence / IT-20260929-074754-emu4411-1                go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / agent FIX log / IT-20260929-074757-emu4212-1            go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / agent evidence / IT-20260929-074757-emu4212-1           go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / emulator FIX log / IT-20260929-074757-emu4212-1         go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / emulator evidence / IT-20260929-074757-emu4212-1        go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / agent FIX log / IT-20260929-074758-emu4413-1            go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / agent evidence / IT-20260929-074758-emu4413-1           go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / emulator FIX log / IT-20260929-074758-emu4413-1         go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / emulator evidence / IT-20260929-074758-emu4413-1        go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent FIX log / IT-20260929-074800-emu4214-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent evidence / IT-20260929-074800-emu4214-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator FIX log / IT-20260929-074800-emu4214-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator evidence / IT-20260929-074800-emu4214-1                go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent FIX log / IT-20260929-074800-emu4214-2                    go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent evidence / IT-20260929-074800-emu4214-2                   go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator FIX log / IT-20260929-074800-emu4214-2                 go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator evidence / IT-20260929-074800-emu4214-2                go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / agent FIX log / IT-20260929-074800-strict15-1            go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / agent evidence / IT-20260929-074800-strict15-1           go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / emulator FIX log / IT-20260929-074800-strict15-1         go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / emulator evidence / IT-20260929-074800-strict15-1        go=PASS py=PASS [all PASS]
  AGREE    A2-6 / agent FIX log / IT-20260929-074801-emu4416-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-6 / agent evidence / IT-20260929-074801-emu4416-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-6 / emulator FIX log / IT-20260929-074801-emu4416-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-6 / emulator evidence / IT-20260929-074801-emu4416-1                go=PASS py=PASS [all PASS]
  AGREE    A2-7 / agent FIX log / IT-20260929-074803-emu4217-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-7 / agent evidence / IT-20260929-074803-emu4217-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-7 / emulator FIX log / IT-20260929-074803-emu4217-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-7 / emulator evidence / IT-20260929-074803-emu4217-1                go=PASS py=PASS [all PASS]
  AGREE    A2-8 / agent FIX log / IT-20260929-074804-emu4418-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-8 / agent evidence / IT-20260929-074804-emu4418-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-8 / emulator FIX log / IT-20260929-074804-emu4418-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-8 / emulator evidence / IT-20260929-074804-emu4418-1                go=PASS py=PASS [all PASS]
  AGREE    A2-9 / agent FIX log / IT-20260929-074806-emu4219-1                    go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    A2-9 / agent evidence / IT-20260929-074806-emu4219-1                   go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    A2-9 / emulator FIX log / IT-20260929-074806-emu4219-1                 go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    A2-9 / emulator evidence / IT-20260929-074806-emu4219-1                go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    A2-10 / agent FIX log / IT-20260929-074807-emu4220-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-10 / agent evidence / IT-20260929-074807-emu4220-1                  go=PASS py=PASS [all PASS]
  AGREE    A2-10 / emulator FIX log / IT-20260929-074807-emu4220-1                go=PASS py=PASS [all PASS]
  AGREE    A2-10 / emulator evidence / IT-20260929-074807-emu4220-1               go=PASS py=PASS [all PASS]
  AGREE    A2-11 / agent FIX log / RAWF-dlrmuztmlkdc                              go=PASS py=PASS [all PASS]
  AGREE    A2-11 / agent evidence / RAWF-dlrmuztmlkdc                             go=PASS py=PASS [all PASS]
  AGREE    A2-11 / emulator FIX log / RAWF-dlrmuztmlkdc                           go=PASS py=PASS [all PASS]
  AGREE    A2-11 / emulator evidence / RAWF-dlrmuztmlkdc                          go=PASS py=PASS [all PASS]
  AGREE    tampered agent FIX log: cum_qty_monotonic (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [cum_qty_monotonic=FAIL working_quantities=FAIL]
  AGREE    tampered agent FIX log: working_quantities (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [working_quantities=FAIL]
  AGREE    tampered agent FIX log: terminal_quantities (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [terminal_quantities=FAIL]
  AGREE    tampered agent FIX log: fill_quantities_sum (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [fill_quantities_sum=FAIL]
  AGREE    tampered agent FIX log: avg_px (IT-20260929-074810-emu4422-1)          go=FAIL py=FAIL [avg_px=FAIL]
  AGREE    tampered agent FIX log: exec_ids_unique (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [exec_ids_unique=FAIL]
  AGREE    tampered agent FIX log: order_id_constant (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    tampered agent FIX log: nothing_after_terminal (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [fill_quantities_sum=FAIL nothing_after_terminal=FAIL]
  AGREE    tampered agent FIX log: version_rules (IT-20260929-074810-emu4422-1)   go=FAIL py=FAIL [version_rules=FAIL]
  AGREE    tampered agent FIX log: version_rules (IT-20260929-074810-emu4422-2)   go=FAIL py=FAIL [version_rules=FAIL]
  AGREE    tampered agent FIX log: requests_answered (IT-20260929-074810-emu4422-2) go=WARN py=WARN [requests_answered=WARN]
  AGREE    tampered agent FIX log: framing_intact (IT-20260929-074810-emu4422-2)  go=WARN py=WARN [framing_intact=WARN]
  AGREE    tampered agent evidence: avg_px (IT-20260929-074810-emu4422-1)         go=FAIL py=FAIL [avg_px=FAIL]
  DIVERGE  divergence 1: own Reject counted as an answer                          go=WARN py=PASS [requests_answered: go WARN, py PASS] (documented, expected)
  DIVERGE  divergence 2: tag 110= truncates the message                           go=PASS py=WARN [framing_intact: go PASS, py WARN] (documented, expected)
  DIVERGE  divergence 3: non-ASCII value breaks the checksum                      go=PASS py=WARN [framing_intact: go PASS, py WARN] (documented, expected)

SCENARIO VERDICTS (Go, live):
  1 full fill FIX.4.2 (AAPL mkt)               IT-20260929-074745-emu428-1    FILLED            PASS 
  1 full fill FIX.4.2 (CSCO lmt day)           IT-20260929-074745-emu428-2    FILLED            PASS 
  1 full fill FIX.4.4 (AAPL mkt)               IT-20260929-074747-emu449-1    FILLED            PASS 
  1 full fill FIX.4.4 (CSCO lmt day)           IT-20260929-074747-emu449-2    FILLED            PASS 
  2 partials EFG (left working)                IT-20260929-074750-emu4210-1   PARTIALLY_FILLED  PASS 
  3 odd lots NOK 1/2/3/405/589                 IT-20260929-074754-emu4411-1   FILLED            PASS 
  4 hold/replace/cancel ZWZZT FIX.4.2          IT-20260929-074757-emu4212-1   CANCELED          PASS 
  4 hold/replace/cancel ZWZZT FIX.4.4          IT-20260929-074758-emu4413-1   CANCELED          PASS 
  5 rule reject KO                             IT-20260929-074800-emu4214-1   REJECTED          PASS 
  5 band reject AAPL lmt 500                   IT-20260929-074800-emu4214-2   REJECTED          PASS 
  5 strict broker rejects all                  IT-20260929-074800-strict15-1  REJECTED          PASS 
  6 unsolicited cancel HON                     IT-20260929-074801-emu4416-1   CANCELED          PASS 
  7 manual fills 300@10 + 200@11               IT-20260929-074803-emu4217-1   PARTIALLY_FILLED  PASS 
  8 ER with 9999=FOO                           IT-20260929-074804-emu4418-1   FILLED            PASS 
  9 duplicate ClOrdID via SendRaw              IT-20260929-074806-emu4219-1   NEW               FAIL order_id_constant=FAIL (the chain carries 2 OrderIDs: O-20260929-074805-1, O-20260929-074805-2)
  10 TestRequest behind gap, then AAPL         IT-20260929-074807-emu4220-1   FILLED            PASS 
  11 cancel unknown ClOrdID via SendRaw        RAWF-dlrmuztmlkdc              (no order)        PASS
  tamper base: NOK odd lots (4.4)              IT-20260929-074810-emu4422-1   FILLED            PASS 
  tamper base: ZWZZT replace/cancel (4.4)      IT-20260929-074810-emu4422-2   CANCELED          PASS 
```
Coverage:
- **85/85 parity cases agree** on verdict, step count, ClOrdID set and every one of the 11 check statuses.
- 68 cases come from the A2 scenarios: each order checked on four files — the agent FIX log, the agent evidence, the emulator FIX log and the emulator evidence.
- 4 cases come from the CLI session.
- 13 are tampered variants. Every check is broken at least once, and Go and Python fail or warn identically.
- 3 are **documented divergences**. They must diverge exactly as described in §6, and they do.

<details><summary>Full <code>make interop</code> output (1138 lines)</summary>

```
go build -o bin/orderecho ./cmd/orderecho
go test -count=1 -tags interop -v ./internal/interop/
=== RUN   TestCLIOrderExitCodes
    cli_test.go:40: emulator up: fix=50455 strict=50456 api=50457 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLIOrderExitCodes3706529780/001/emulator
    cli_test.go:43: $ orderecho order --session emu44 AAPL 100 buy mkt  -> exit 0
        OrderEcho agent 0.2.0 (a2) - session emu44
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:50455
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074635.jsonl
          fix log        : logs/fix/emu44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:46:35.222 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:50455 evidence=data/evidence/20260929-074635.jsonl
        20260929-07:46:35.223 INFO    session  emu44  Connecting to 127.0.0.1:50455
        20260929-07:46:35.223 INFO    session  emu44  Connected to 127.0.0.1:50455 (local 127.0.0.1:50616)
        20260929-07:46:35.303 INFO    session  emu44  connected: sending Logon
        20260929-07:46:35.303 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
        20260929-07:46:35.303 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:46:35.302|98=0|108=30|141=Y|10=069|
        20260929-07:46:35.303 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
        20260929-07:46:35.307 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:46:35.306|98=0|108=30|141=Y|10=073|
        20260929-07:46:35.310 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:46:35.310 INFO    session  emu44  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-07:46:35.324 OUT  seq=2    35=D  8=FIX.4.4|9=136|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:46:35.324|11=OE-20260929-074635-1|21=1|55=AAPL|54=1|60=20260929-07:46:35.310|38=100|40=1|10=083|
        >> D 11=OE-20260929-074635-1 BUY 100 AAPL MKT sent
        20260929-07:46:35.328 IN   seq=2    35=8  8=FIX.4.4|9=223|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:46:35.327|37=O-20260929-074630-1|11=OE-20260929-074635-1|17=E-20260929-074630-1|150=0|39=0|55=AAPL|54=1|38=100|40=1|32=0|31=0.00|151=100|14=0|6=0.0000|60=20260929-07:46:35.326|10=078|
        20260929-07:46:35.331 INFO    session  emu44  application message received: 35=8 (ExecutionReport) seq=2
        20260929-07:46:35.331 INFO    session  emu44  << ER 11=OE-20260929-074635-1 37=O-20260929-074630-1 150=0(New) 39=0(New) cum=0 leaves=100 avg=0.0000  -> NEW cum=0 leaves=100  checks: PASS
        20260929-07:46:35.914 IN   seq=3    35=8  8=FIX.4.4|9=229|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:46:35.913|37=O-20260929-074630-1|11=OE-20260929-074635-1|17=E-20260929-074630-2|150=F|39=2|55=AAPL|54=1|38=100|40=1|32=100|31=227.50|151=0|14=100|6=227.5000|60=20260929-07:46:35.912|10=177|
        20260929-07:46:35.917 INFO    session  emu44  application message received: 35=8 (ExecutionReport) seq=3
        20260929-07:46:35.917 INFO    session  emu44  << ER 11=OE-20260929-074635-1 37=O-20260929-074630-1 150=F(Trade) 39=2(Filled) last=100@227.50 cum=100 leaves=0 avg=227.5000  -> FILLED cum=100 leaves=0  checks: PASS
        
        Order chain for OE-20260929-074635-1
          ClOrdIDs: OE-20260929-074635-1
          OrderID : O-20260929-074630-1
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T07:46:35.324 <--  NewOrderSingle       - / -                            100              -       -       -          -
          2026-09-29T07:46:35.331 -->  ExecutionReport      0 (New) / 0 (New)                100              -       0     100     0.0000
          2026-09-29T07:46:35.917 -->  ExecutionReport      F (Trade) / 2 (Filled)           100     100@227.50     100       0   227.5000
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 100 without ever falling
          [PASS] working_quantities: 1 working report(s) balanced
          [PASS] terminal_quantities: 1 terminal report(s) consistent
          [PASS] fill_quantities_sum: 1 fill(s) totalling 100 match CumQty
          [PASS] avg_px: AvgPx 227.5000 matches the fills to within 0.0001
          [PASS] exec_ids_unique: 2 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-074630-1 throughout
          [PASS] nothing_after_terminal: terminal 39=2 was the last word
          [PASS] version_rules: every report matches its version's conventions
          [PASS] requests_answered: all 1 request(s) answered
          [PASS] framing_intact: all 3 message(s) correctly framed
        
          verdict: PASS
        20260929-07:46:35.931 OUT  seq=3    35=5  8=FIX.4.4|9=88|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-07:46:35.931|58=OrderEcho agent: order done|10=146|
        20260929-07:46:35.931 INFO    session  emu44  logout initiated: OrderEcho agent: order done
        20260929-07:46:35.931 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
        20260929-07:46:35.933 IN   seq=4    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=4|52=20260929-07:46:35.933|58=Logout acknowledged|10=041|
        20260929-07:46:35.937 INFO    session  emu44  logout confirmed: Logout acknowledged
        20260929-07:46:35.937 INFO    session  emu44  Disconnecting: Logout confirmed
        20260929-07:46:35.941 INFO    session  emu44  disconnected: next_out=4 next_in=5
        20260929-07:46:35.941 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
        20260929-07:46:35.941 INFO    session  emu44  Connection closed, peer=127.0.0.1:50455
        20260929-07:46:35.941 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    cli_test.go:53: $ orderecho order --session emu42 EFG 1000 buy lmt 10.00 --wait 2500ms  -> exit 6
        OrderEcho agent 0.2.0 (a2) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:50455
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074635.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:46:35.949 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:50455 evidence=data/evidence/20260929-074635.jsonl
        20260929-07:46:35.949 INFO    session  emu42  Connecting to 127.0.0.1:50455
        20260929-07:46:35.949 INFO    session  emu42  Connected to 127.0.0.1:50455 (local 127.0.0.1:50617)
        20260929-07:46:35.960 INFO    session  emu42  connected: sending Logon
        20260929-07:46:35.960 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-07:46:35.960 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:46:35.960|98=0|108=30|141=Y|10=077|
        20260929-07:46:35.960 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-07:46:35.964 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:46:35.964|98=0|108=30|141=Y|10=081|
        20260929-07:46:35.968 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:46:35.968 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-07:46:35.971 OUT  seq=2    35=D  8=FIX.4.2|9=145|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:46:35.971|11=OE-20260929-074635-1|21=1|55=EFG|54=1|60=20260929-07:46:35.968|38=1000|40=2|44=10.00|10=230|
        >> D 11=OE-20260929-074635-1 BUY 1000 EFG LMT 10.00 sent
        20260929-07:46:35.974 IN   seq=2    35=8  8=FIX.4.2|9=238|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:46:35.974|37=O-20260929-074630-2|11=OE-20260929-074635-1|17=E-20260929-074630-3|20=0|150=0|39=0|55=EFG|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-07:46:35.973|10=223|
        20260929-07:46:35.978 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=2
        20260929-07:46:35.978 INFO    session  emu42  << ER 11=OE-20260929-074635-1 37=O-20260929-074630-2 150=0(New) 39=0(New) cum=0 leaves=1000 avg=0.0000  -> NEW cum=0 leaves=1000  checks: PASS
        20260929-07:46:36.576 IN   seq=3    35=8  8=FIX.4.2|9=243|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:46:36.576|37=O-20260929-074630-2|11=OE-20260929-074635-1|17=E-20260929-074630-4|20=0|150=1|39=1|55=EFG|54=1|38=1000|40=2|44=10.00|32=400|31=10.00|151=600|14=400|6=10.0000|60=20260929-07:46:36.574|10=219|
        20260929-07:46:36.581 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=3
        20260929-07:46:36.581 INFO    session  emu42  << ER 11=OE-20260929-074635-1 37=O-20260929-074630-2 150=1(Partial fill) 39=1(Partially filled) last=400@10.00 cum=400 leaves=600 avg=10.0000  -> PARTIALLY_FILLED cum=400 leaves=600  checks: PASS
        20260929-07:46:36.986 IN   seq=4    35=8  8=FIX.4.2|9=243|35=8|49=ORDERECHO|56=AGENT|34=4|52=20260929-07:46:36.985|37=O-20260929-074630-2|11=OE-20260929-074635-1|17=E-20260929-074630-5|20=0|150=1|39=1|55=EFG|54=1|38=1000|40=2|44=10.00|32=100|31=10.00|151=500|14=500|6=10.0000|60=20260929-07:46:36.984|10=227|
        20260929-07:46:37.046 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=4
        20260929-07:46:37.046 INFO    session  emu42  << ER 11=OE-20260929-074635-1 37=O-20260929-074630-2 150=1(Partial fill) 39=1(Partially filled) last=100@10.00 cum=500 leaves=500 avg=10.0000  -> PARTIALLY_FILLED cum=500 leaves=500  checks: PASS
        >>> order OE-20260929-074635-1 still PARTIALLY_FILLED after 2.5s
        
        Order chain for OE-20260929-074635-1
          ClOrdIDs: OE-20260929-074635-1
          OrderID : O-20260929-074630-2
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T07:46:35.971 <--  NewOrderSingle       - / -                           1000              -       -       -          -
          2026-09-29T07:46:35.978 -->  ExecutionReport      0 (New) / 0 (New)               1000              -       0    1000     0.0000
          2026-09-29T07:46:36.580 -->  ExecutionReport      1 (Partial fill) / 1 (Partially filled)    1000      400@10.00     400     600    10.0000
          2026-09-29T07:46:37.045 -->  ExecutionReport      1 (Partial fill) / 1 (Partially filled)    1000      100@10.00     500     500    10.0000
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 500 without ever falling
          [PASS] working_quantities: 3 working report(s) balanced
          [PASS] terminal_quantities: no terminal reports to check
          [PASS] fill_quantities_sum: 2 fill(s) totalling 500 match CumQty
          [PASS] avg_px: AvgPx 10.0000 matches the fills to within 0.0001
          [PASS] exec_ids_unique: 3 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-074630-2 throughout
          [PASS] nothing_after_terminal: the order never reached a terminal state
          [PASS] version_rules: every report matches its version's conventions
          [PASS] requests_answered: all 1 request(s) answered
          [PASS] framing_intact: all 4 message(s) correctly framed
        
          verdict: PASS
        20260929-07:46:38.636 OUT  seq=3    35=5  8=FIX.4.2|9=88|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-07:46:38.636|58=OrderEcho agent: order done|10=149|
        20260929-07:46:38.636 INFO    session  emu42  logout initiated: OrderEcho agent: order done
        20260929-07:46:38.636 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-07:46:38.638 IN   seq=5    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=5|52=20260929-07:46:38.638|58=Logout acknowledged|10=045|
        20260929-07:46:38.642 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-07:46:38.642 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-07:46:38.646 INFO    session  emu42  disconnected: next_out=4 next_in=6
        20260929-07:46:38.646 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-07:46:38.646 INFO    session  emu42  Connection closed, peer=127.0.0.1:50455
        20260929-07:46:38.646 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    cli_test.go:80: POST /sessions/agent42/inject/next -> {"queued":{"id":1,"msg_type":"8","set":{"14":"999"},"remove":[],"corrupt_checksum":false,"count":1,"remaining":1}}
    cli_test.go:81: POST /orders/O-20260929-074630-3/fill-rest -> {"session":"agent42","order":{"order_id":"O-20260929-074630-3","cl_ord_id":"OE-20260929-074638-1","symbol":"ZWZZT","side":"1","order_qty":"1000","cum_qty":"1000","leaves_qty":"0","avg_px":"10.0000","ord_status":"2","price_source":"limit","rule_name":"nasdaq-test-hold"},"sent":[{"seq":3,"msg_type":"8","raw":"8=FIX.4.2|9=244|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:46:38.761|37=O-20260929-074630-3|11=OE-20260929-074638-1|17=E-20260929-074630-7|20=0|150=2|39=2|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=1000|31=10.00|151=0|14=999|6=10.0000|60=20260929-07:46:38.760|10=167|","injected":true}]}
    cli_test.go:62: $ orderecho order --session emu42 ZWZZT 1000 buy lmt 10.00 --wait 20s  -> exit 5
        OrderEcho agent 0.2.0 (a2) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:50455
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=4 next_in=6)
          evidence file  : data/evidence/20260929-074638.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:46:38.654 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:50455 evidence=data/evidence/20260929-074638.jsonl
        20260929-07:46:38.655 INFO    session  emu42  Connecting to 127.0.0.1:50455
        20260929-07:46:38.655 INFO    session  emu42  Connected to 127.0.0.1:50455 (local 127.0.0.1:50619)
        20260929-07:46:38.717 INFO    session  emu42  connected: sending Logon
        20260929-07:46:38.717 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-07:46:38.717 INFO    session  emu42  store archived: outbound message store archived to data/msgstore/emu42.jsonl.20260929-074638 (Logon will carry 141=Y)
        20260929-07:46:38.717 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:46:38.717|98=0|108=30|141=Y|10=080|
        20260929-07:46:38.717 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-07:46:38.721 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:46:38.721|98=0|108=30|141=Y|10=075|
        20260929-07:46:38.728 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:46:38.728 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-07:46:38.732 OUT  seq=2    35=D  8=FIX.4.2|9=147|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:46:38.732|11=OE-20260929-074638-1|21=1|55=ZWZZT|54=1|60=20260929-07:46:38.729|38=1000|40=2|44=10.00|10=206|
        >> D 11=OE-20260929-074638-1 BUY 1000 ZWZZT LMT 10.00 sent
        20260929-07:46:38.735 IN   seq=2    35=8  8=FIX.4.2|9=240|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:46:38.735|37=O-20260929-074630-3|11=OE-20260929-074638-1|17=E-20260929-074630-6|20=0|150=0|39=0|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-07:46:38.733|10=193|
        20260929-07:46:38.740 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=2
        20260929-07:46:38.740 INFO    session  emu42  << ER 11=OE-20260929-074638-1 37=O-20260929-074630-3 150=0(New) 39=0(New) cum=0 leaves=1000 avg=0.0000  -> NEW cum=0 leaves=1000  checks: PASS
        20260929-07:46:38.762 IN   seq=3    35=8  8=FIX.4.2|9=244|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:46:38.761|37=O-20260929-074630-3|11=OE-20260929-074638-1|17=E-20260929-074630-7|20=0|150=2|39=2|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=1000|31=10.00|151=0|14=999|6=10.0000|60=20260929-07:46:38.760|10=167|
        20260929-07:46:38.767 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=3
        20260929-07:46:38.767 INFO    session  emu42  << ER 11=OE-20260929-074638-1 37=O-20260929-074630-3 150=2(Fill) 39=2(Filled) last=1000@10.00 cum=999 leaves=0 avg=10.0000  -> FILLED cum=1000 leaves=0  checks: FAIL
        20260929-07:46:38.767 ERROR   session  emu42  check terminal_quantities: FAIL terminal_quantities: 39=2 (Filled) with CumQty 999 but OrderQty 1000, at seq=3 17=E-20260929-074630-7 (order OE-20260929-074638-1)
        20260929-07:46:38.767 ERROR   session  emu42  check fill_quantities_sum: FAIL fill_quantities_sum: 1 fill(s) totalling 1000 but the last CumQty is 999 (order OE-20260929-074638-1)
        
        Order chain for OE-20260929-074638-1
          ClOrdIDs: OE-20260929-074638-1
          OrderID : O-20260929-074630-3
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T07:46:38.732 <--  NewOrderSingle       - / -                           1000              -       -       -          -
          2026-09-29T07:46:38.739 -->  ExecutionReport      0 (New) / 0 (New)               1000              -       0    1000     0.0000
          2026-09-29T07:46:38.766 -->  ExecutionReport      2 (Fill) / 2 (Filled)           1000     1000@10.00     999       0    10.0000
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 999 without ever falling
          [PASS] working_quantities: 1 working report(s) balanced
          [FAIL] terminal_quantities: 39=2 (Filled) with CumQty 999 but OrderQty 1000, at seq=3 17=E-20260929-074630-7
          [FAIL] fill_quantities_sum: 1 fill(s) totalling 1000 but the last CumQty is 999
          [PASS] avg_px: AvgPx 10.0000 matches the fills to within 0.0001
          [PASS] exec_ids_unique: 2 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-074630-3 throughout
          [PASS] nothing_after_terminal: terminal 39=2 was the last word
          [PASS] version_rules: every report matches its version's conventions
          [PASS] requests_answered: all 1 request(s) answered
          [PASS] framing_intact: all 3 message(s) correctly framed
        
          verdict: FAIL
        20260929-07:46:38.788 OUT  seq=3    35=5  8=FIX.4.2|9=88|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-07:46:38.788|58=OrderEcho agent: order done|10=157|
        20260929-07:46:38.788 INFO    session  emu42  logout initiated: OrderEcho agent: order done
        20260929-07:46:38.788 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-07:46:38.790 IN   seq=4    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=4|52=20260929-07:46:38.790|58=Logout acknowledged|10=043|
        20260929-07:46:38.795 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-07:46:38.795 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-07:46:38.799 INFO    session  emu42  disconnected: next_out=4 next_in=5
        20260929-07:46:38.799 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-07:46:38.799 INFO    session  emu42  Connection closed, peer=127.0.0.1:50455
        20260929-07:46:38.799 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    cli_test.go:87: $ orderecho order --session strict --fix-version FIX.4.4 AAPL 100 buy mkt  -> exit 1
        OrderEcho agent 0.2.0 (a2) - session strict
          config         : orderecho.yaml
          route          : AGENT -> STRICTBRK  FIX.4.4  127.0.0.1:50456
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/strict.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074638.jsonl
          fix log        : logs/fix/strict_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:46:38.806 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=strict FIX.4.4 AGENT->STRICTBRK@127.0.0.1:50456 evidence=data/evidence/20260929-074638.jsonl
        20260929-07:46:38.807 INFO    session  strict  Connecting to 127.0.0.1:50456
        20260929-07:46:38.807 INFO    session  strict  Connected to 127.0.0.1:50456 (local 127.0.0.1:50624)
        20260929-07:46:38.852 INFO    session  strict  connected: sending Logon
        20260929-07:46:38.852 INFO    session  strict  seqnums reset: Logon will carry 141=Y
        20260929-07:46:38.852 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-07:46:38.852|98=0|108=30|141=Y|10=111|
        20260929-07:46:38.852 INFO    session  strict  State DISCONNECTED -> LOGON_SENT
        20260929-07:46:38.854 IN   seq=1    35=5  8=FIX.4.2|9=100|35=5|49=STRICTBRK|56=AGENT|34=1|52=20260929-07:46:38.854|58=Incorrect BeginString, expected FIX.4.2|10=121|
        20260929-07:46:38.859 WARNING session  strict  logon refused: counterparty answered Logon with Logout: Incorrect BeginString, expected FIX.4.2
        20260929-07:46:38.859 OUT  seq=2    35=5  8=FIX.4.4|9=80|35=5|49=AGENT|56=STRICTBRK|34=2|52=20260929-07:46:38.858|58=Logout acknowledged|10=077|
        20260929-07:46:38.859 INFO    session  strict  Disconnecting: Logon refused by counterparty: Incorrect BeginString, expected FIX.4.2
        20260929-07:46:38.863 INFO    session  strict  disconnected: next_out=3 next_in=1
        20260929-07:46:38.863 INFO    session  strict  State LOGON_SENT -> DISCONNECTED
        20260929-07:46:38.863 INFO    session  strict  Connection closed, peer=127.0.0.1:50456
        20260929-07:46:38.863 INFO    session  engine  Shutdown complete
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
--- PASS: TestCLIOrderExitCodes (21.15s)
=== RUN   TestCLISessionPipedAndTimeline
    cli_test.go:109: emulator up: fix=50625 strict=50626 api=50627 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLISessionPipedAndTimeline2097211448/001/emulator
    cli_test.go:112: $ orderecho session --session emu42  -> exit 0
        OrderEcho agent 0.2.0 (a2) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:50625
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074653.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:46:53.164 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:50625 evidence=data/evidence/20260929-074653.jsonl
        20260929-07:46:53.165 INFO    session  emu42  Connecting to 127.0.0.1:50625
        20260929-07:46:53.165 INFO    session  emu42  Connected to 127.0.0.1:50625 (local 127.0.0.1:50761)
        20260929-07:46:53.195 INFO    session  emu42  connected: sending Logon
        20260929-07:46:53.195 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-07:46:53.195 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:46:53.194|98=0|108=30|141=Y|10=076|
        20260929-07:46:53.197 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-07:46:53.201 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:46:53.200|98=0|108=30|141=Y|10=064|
        20260929-07:46:53.205 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:46:53.205 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        >>> session ready; type "help" for commands
        > order EFG 1000 buy lmt 10.00
        20260929-07:46:53.209 OUT  seq=2    35=D  8=FIX.4.2|9=145|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:46:53.209|11=OE-20260929-074653-1|21=1|55=EFG|54=1|60=20260929-07:46:53.205|38=1000|40=2|44=10.00|10=208|
        >> D 11=OE-20260929-074653-1 BUY 1000 EFG LMT 10.00 sent
        20260929-07:46:53.212 IN   seq=2    35=8  8=FIX.4.2|9=238|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:46:53.212|37=O-20260929-074650-1|11=OE-20260929-074653-1|17=E-20260929-074650-1|20=0|150=0|39=0|55=EFG|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-07:46:53.211|10=194|
        20260929-07:46:53.218 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=2
        20260929-07:46:53.218 INFO    session  emu42  << ER 11=OE-20260929-074653-1 37=O-20260929-074650-1 150=0(New) 39=0(New) cum=0 leaves=1000 avg=0.0000  -> NEW cum=0 leaves=1000  checks: PASS
        > order ZWZZT 500 buy lmt 10.00
        20260929-07:46:53.274 OUT  seq=3    35=D  8=FIX.4.2|9=146|35=D|49=AGENT|56=ORDERECHO|34=3|52=20260929-07:46:53.274|11=OE-20260929-074653-2|21=1|55=ZWZZT|54=1|60=20260929-07:46:53.268|38=500|40=2|44=10.00|10=153|
        >> D 11=OE-20260929-074653-2 BUY 500 ZWZZT LMT 10.00 sent
        20260929-07:46:53.278 IN   seq=3    35=8  8=FIX.4.2|9=238|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:46:53.278|37=O-20260929-074650-2|11=OE-20260929-074653-2|17=E-20260929-074650-2|20=0|150=0|39=0|55=ZWZZT|54=1|38=500|40=2|44=10.00|32=0|31=0.00|151=500|14=0|6=0.0000|60=20260929-07:46:53.276|10=108|
        20260929-07:46:53.282 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=3
        20260929-07:46:53.282 INFO    session  emu42  << ER 11=OE-20260929-074653-2 37=O-20260929-074650-2 150=0(New) 39=0(New) cum=0 leaves=500 avg=0.0000  -> NEW cum=0 leaves=500  checks: PASS
        > replace last 800 10.50
        20260929-07:46:53.337 OUT  seq=4    35=G  8=FIX.4.2|9=193|35=G|49=AGENT|56=ORDERECHO|34=4|52=20260929-07:46:53.337|41=OE-20260929-074653-2|11=OE-20260929-074653-3|37=O-20260929-074650-2|21=1|55=ZWZZT|54=1|60=20260929-07:46:53.333|38=800|40=2|44=10.50|10=236|
        >> G 11=OE-20260929-074653-3 41=OE-20260929-074653-2 qty=800 10.50 sent
        20260929-07:46:53.341 IN   seq=4    35=8  8=FIX.4.2|9=262|35=8|49=ORDERECHO|56=AGENT|34=4|52=20260929-07:46:53.340|37=O-20260929-074650-2|11=OE-20260929-074653-3|41=OE-20260929-074653-2|17=E-20260929-074650-3|20=0|150=5|39=0|55=ZWZZT|54=1|38=800|40=2|44=10.50|32=0|31=0.00|151=800|14=0|6=0.0000|60=20260929-07:46:53.339|10=057|
        20260929-07:46:53.345 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=4
        20260929-07:46:53.345 INFO    session  emu42  << ER 11=OE-20260929-074653-3 37=O-20260929-074650-2 150=5(Replaced) 39=0(New) cum=0 leaves=800 avg=0.0000  -> NEW cum=0 leaves=800  checks: PASS
        > cancel last
        20260929-07:46:53.403 OUT  seq=5    35=F  8=FIX.4.2|9=174|35=F|49=AGENT|56=ORDERECHO|34=5|52=20260929-07:46:53.403|41=OE-20260929-074653-3|11=OE-20260929-074653-4|37=O-20260929-074650-2|55=ZWZZT|54=1|60=20260929-07:46:53.397|38=800|10=177|
        >> F 11=OE-20260929-074653-4 41=OE-20260929-074653-3 sent
        20260929-07:46:53.406 IN   seq=5    35=8  8=FIX.4.2|9=260|35=8|49=ORDERECHO|56=AGENT|34=5|52=20260929-07:46:53.406|37=O-20260929-074650-2|11=OE-20260929-074653-4|41=OE-20260929-074653-3|17=E-20260929-074650-4|20=0|150=4|39=4|55=ZWZZT|54=1|38=800|40=2|44=10.50|32=0|31=0.00|151=0|14=0|6=0.0000|60=20260929-07:46:53.405|10=211|
        20260929-07:46:53.410 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=5
        20260929-07:46:53.410 INFO    session  emu42  << ER 11=OE-20260929-074653-4 37=O-20260929-074650-2 150=4(Canceled) 39=4(Canceled) cum=0 leaves=0 avg=0.0000  -> CANCELED cum=0 leaves=0  checks: PASS
        > status
        CLORDID                      ORDERID                SYMBOL SIDE  TYPE      QTY      CUM   LEAVES        AVG STATE / CHECKS
        OE-20260929-074653-1         O-20260929-074650-1    EFG    BUY   LMT      1000        0     1000     0.0000 NEW / PASS
        OE-20260929-074653-4         O-20260929-074650-2    ZWZZT  BUY   LMT       800        0        0     0.0000 CANCELED / PASS
        > timeline last
        Order chain for OE-20260929-074653-2
          ClOrdIDs: OE-20260929-074653-2, OE-20260929-074653-3, OE-20260929-074653-4
          OrderID : O-20260929-074650-2
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T07:46:53.274 <--  NewOrderSingle       - / -                            500              -       -       -          -
          2026-09-29T07:46:53.282 -->  ExecutionReport      0 (New) / 0 (New)                500              -       0     500     0.0000
          2026-09-29T07:46:53.337 <--  OrderCancelReplaceRequest - / -                            800              -       -       -          -
          2026-09-29T07:46:53.345 -->  ExecutionReport      5 (Replaced) / 0 (New)           800              -       0     800     0.0000
          2026-09-29T07:46:53.403 <--  OrderCancelRequest   - / -                            800              -       -       -          -
          2026-09-29T07:46:53.410 -->  ExecutionReport      4 (Canceled) / 4 (Canceled)      800              -       0       0     0.0000
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 0 without ever falling
          [PASS] working_quantities: 2 working report(s) balanced
          [PASS] terminal_quantities: 1 terminal report(s) consistent
          [PASS] fill_quantities_sum: no fills in this chain
          [PASS] avg_px: no fills to average
          [PASS] exec_ids_unique: 3 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-074650-2 throughout
          [PASS] nothing_after_terminal: terminal 39=4 was the last word
          [PASS] version_rules: every report matches its version's conventions
          [PASS] requests_answered: all 3 request(s) answered
          [PASS] framing_intact: all 6 message(s) correctly framed
        
          verdict: PASS
        > resend 1 2
        20260929-07:46:53.473 INFO    session  emu42  resend request sent: 7=1 16=2 (on request)
        20260929-07:46:53.473 OUT  seq=6    35=2  8=FIX.4.2|9=66|35=2|49=AGENT|56=ORDERECHO|34=6|52=20260929-07:46:53.473|7=1|16=2|10=121|
        20260929-07:46:53.473 INFO    session  emu42  ResendRequest sent: 7=1 16=2
        >> ResendRequest 7=1 16=2 sent
        > testreq
        20260929-07:46:53.478 OUT  seq=7    35=1  8=FIX.4.2|9=68|35=1|49=AGENT|56=ORDERECHO|34=7|52=20260929-07:46:53.478|112=TEST-1|10=115|
        >> TestRequest TEST-1 sent
        20260929-07:46:53.479 IN   seq=1    35=4  8=FIX.4.2|9=99|35=4|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:46:53.478|43=Y|122=20260929-07:46:53.478|123=Y|36=2|10=036|
        20260929-07:46:53.479 WARNING session  emu42  possdup ignored: MsgSeqNum 1 below expected 6, 43=Y
        20260929-07:46:53.480 IN   seq=2    35=8  8=FIX.4.2|9=269|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:46:53.479|43=Y|122=20260929-07:46:53.212|37=O-20260929-074650-1|11=OE-20260929-074653-1|17=E-20260929-074650-1|20=0|150=0|39=0|55=EFG|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-07:46:53.211|10=225|
        20260929-07:46:53.480 WARNING session  emu42  possdup ignored: MsgSeqNum 2 below expected 6, 43=Y
        20260929-07:46:53.482 IN   seq=6    35=0  8=FIX.4.2|9=68|35=0|49=ORDERECHO|56=AGENT|34=6|52=20260929-07:46:53.482|112=TEST-1|10=108|
        20260929-07:46:53.487 INFO    session  emu42  testrequest answered: 112=TEST-1
        >> TestRequest TEST-1 answered
        > quit
        20260929-07:46:53.491 OUT  seq=8    35=5  8=FIX.4.2|9=90|35=5|49=AGENT|56=ORDERECHO|34=8|52=20260929-07:46:53.491|58=OrderEcho agent: session done|10=119|
        20260929-07:46:53.491 INFO    session  emu42  logout initiated: OrderEcho agent: session done
        20260929-07:46:53.491 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-07:46:53.494 IN   seq=7    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=7|52=20260929-07:46:53.494|58=Logout acknowledged|10=044|
        20260929-07:46:53.499 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-07:46:53.499 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-07:46:53.503 INFO    session  emu42  disconnected: next_out=9 next_in=8
        20260929-07:46:53.503 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-07:46:53.503 INFO    session  emu42  Connection closed, peer=127.0.0.1:50625
        20260929-07:46:53.503 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    cli_test.go:127: $ orderecho timeline /var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLISessionPipedAndTimeline2097211448/002/cli/logs/fix/emu42_20260929.log --clordid OE-20260929-074653-3 --json  -> exit 0
        {
          "seed": "OE-20260929-074653-3",
          "cl_ord_ids": [
            "OE-20260929-074653-2",
            "OE-20260929-074653-3",
            "OE-20260929-074653-4"
          ],
          "order_ids": [
            "O-20260929-074650-2"
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
              "explanation": "OrderID O-20260929-074650-2 throughout",
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
    cli_test.go:142: $ orderecho timeline /var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLISessionPipedAndTimeline2097211448/003/emu42_20260929.log --clordid OE-20260929-074653-3  -> exit 1
        Order chain for OE-20260929-074653-3
          ClOrdIDs: OE-20260929-074653-2, OE-20260929-074653-3, OE-20260929-074653-4
          OrderID : O-20260929-074650-2
        
          time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
          2026-09-29T07:46:53.274 <--  NewOrderSingle       - / -                            500              -       -       -          -
          2026-09-29T07:46:53.278 -->  ExecutionReport      0 (New) / 0 (New)                500              -       0     500     0.0000
          2026-09-29T07:46:53.337 <--  OrderCancelReplaceRequest - / -                            800              -       -       -          -
          2026-09-29T07:46:53.341 -->  ExecutionReport      5 (Replaced) / 0 (New)           800              -       0     800     0.0000
          2026-09-29T07:46:53.403 <--  OrderCancelRequest   - / -                            800              -       -       -          -
        
        Checks
          [PASS] cum_qty_monotonic: CumQty rose to 0 without ever falling
          [PASS] working_quantities: 2 working report(s) balanced
          [PASS] terminal_quantities: no terminal reports to check
          [PASS] fill_quantities_sum: no fills in this chain
          [PASS] avg_px: no fills to average
          [PASS] exec_ids_unique: 2 ExecID(s), all distinct
          [PASS] order_id_constant: OrderID O-20260929-074650-2 throughout
          [PASS] nothing_after_terminal: the order never reached a terminal state
          [PASS] version_rules: every report matches its version's conventions
          [WARN] requests_answered: 1 request(s) with no response in this log: 35=F seq=5 11=OE-20260929-074653-4
          [PASS] framing_intact: all 5 message(s) correctly framed
        
          verdict: WARN
    cli_test.go:145: $ orderecho timeline /var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCLISessionPipedAndTimeline2097211448/002/cli/logs/fix/emu42_20260929.log --clordid NOPE  -> exit 2
        no messages found for NOPE
    cli_test.go:158: parity CLI session / agent FIX log / OE-20260929-074653-1: PASS (all PASS)
    cli_test.go:158: parity CLI session / emulator FIX log / OE-20260929-074653-1: PASS (all PASS)
    cli_test.go:158: parity CLI session / agent FIX log / OE-20260929-074653-2: PASS (all PASS)
    cli_test.go:158: parity CLI session / emulator FIX log / OE-20260929-074653-2: PASS (all PASS)
--- PASS: TestCLISessionPipedAndTimeline (16.83s)
=== RUN   TestScenario1LogonTestRequestLogout
    interop_test.go:26: emulator up: fix=50762 strict=50763 api=50764 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario1LogonTestRequestLogout1354402490/001/emulator
    interop_test.go:34: emu42: TestRequest TEST-1 answered
    interop_test.go:35: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:51: emu44: TestRequest TEST-1 answered
    interop_test.go:52: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:53: POST /sessions/agent44/logout -> {"state":"LOGOUT_SENT","sent":[{"seq":3,"msg_type":"5","raw":"8=FIX.4.4|9=85|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:47:13.670|58=interop: emulator logout|10=036|","injected":false}]}
    interop_test.go:75: $ orderecho connect --session emu42 --test-request --duration 1s  -> exit 0
        OrderEcho agent 0.2.0 (a2) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:50762
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074713.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:47:13.798 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:50762 evidence=data/evidence/20260929-074713.jsonl
        20260929-07:47:13.803 INFO    session  emu42  Connecting to 127.0.0.1:50762
        20260929-07:47:13.804 INFO    session  emu42  Connected to 127.0.0.1:50762 (local 127.0.0.1:50938)
        20260929-07:47:13.899 INFO    session  emu42  connected: sending Logon
        20260929-07:47:13.899 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-07:47:13.899 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:47:13.899|98=0|108=30|141=Y|10=085|
        20260929-07:47:13.899 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-07:47:13.904 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:47:13.904|98=0|108=30|141=Y|10=072|
        20260929-07:47:14.011 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:47:14.011 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-07:47:14.017 OUT  seq=2    35=1  8=FIX.4.2|9=68|35=1|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:47:14.017|112=TEST-1|10=097|
        >>> TestRequest TEST-1 sent
        20260929-07:47:14.020 IN   seq=2    35=0  8=FIX.4.2|9=68|35=0|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:47:14.019|112=TEST-1|10=098|
        20260929-07:47:14.027 INFO    session  emu42  testrequest answered: 112=TEST-1
        >>> TestRequest TEST-1 answered in 10ms
        >>> Duration 1s elapsed; logging out
        20260929-07:47:15.025 OUT  seq=3    35=5  8=FIX.4.2|9=94|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-07:47:15.025|58=OrderEcho agent: duration elapsed|10=008|
        20260929-07:47:15.025 INFO    session  emu42  logout initiated: OrderEcho agent: duration elapsed
        20260929-07:47:15.025 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-07:47:15.028 IN   seq=3    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:47:15.028|58=Logout acknowledged|10=032|
        20260929-07:47:15.034 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-07:47:15.034 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-07:47:15.038 INFO    session  emu42  disconnected: next_out=4 next_in=4
        20260929-07:47:15.038 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-07:47:15.038 INFO    session  emu42  Connection closed, peer=127.0.0.1:50762
        20260929-07:47:15.038 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    interop_test.go:75: $ orderecho connect --session emu44 --test-request --duration 1s  -> exit 0
        OrderEcho agent 0.2.0 (a2) - session emu44
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:50762
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074715.jsonl
          fix log        : logs/fix/emu44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:47:15.046 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:50762 evidence=data/evidence/20260929-074715.jsonl
        20260929-07:47:15.047 INFO    session  emu44  Connecting to 127.0.0.1:50762
        20260929-07:47:15.047 INFO    session  emu44  Connected to 127.0.0.1:50762 (local 127.0.0.1:50939)
        20260929-07:47:15.144 INFO    session  emu44  connected: sending Logon
        20260929-07:47:15.144 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
        20260929-07:47:15.145 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:47:15.144|98=0|108=30|141=Y|10=072|
        20260929-07:47:15.145 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
        20260929-07:47:15.149 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:47:15.149|98=0|108=30|141=Y|10=077|
        20260929-07:47:15.154 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:47:15.154 INFO    session  emu44  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-07:47:15.157 OUT  seq=2    35=1  8=FIX.4.4|9=68|35=1|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:47:15.157|112=TEST-1|10=105|
        >>> TestRequest TEST-1 sent
        20260929-07:47:15.161 IN   seq=2    35=0  8=FIX.4.4|9=68|35=0|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:47:15.161|112=TEST-1|10=099|
        20260929-07:47:15.166 INFO    session  emu44  testrequest answered: 112=TEST-1
        >>> TestRequest TEST-1 answered in 9ms
        >>> Duration 1s elapsed; logging out
        20260929-07:47:16.207 OUT  seq=3    35=5  8=FIX.4.4|9=94|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-07:47:16.207|58=OrderEcho agent: duration elapsed|10=013|
        20260929-07:47:16.207 INFO    session  emu44  logout initiated: OrderEcho agent: duration elapsed
        20260929-07:47:16.207 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
        20260929-07:47:16.209 IN   seq=3    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:47:16.209|58=Logout acknowledged|10=036|
        20260929-07:47:16.213 INFO    session  emu44  logout confirmed: Logout acknowledged
        20260929-07:47:16.213 INFO    session  emu44  Disconnecting: Logout confirmed
        20260929-07:47:16.221 INFO    session  emu44  disconnected: next_out=4 next_in=4
        20260929-07:47:16.221 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
        20260929-07:47:16.221 INFO    session  emu44  Connection closed, peer=127.0.0.1:50762
        20260929-07:47:16.221 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    interop_test.go:89: POST /sessions/agent42/logout -> {"state":"LOGOUT_SENT","sent":[{"seq":2,"msg_type":"5","raw":"8=FIX.4.2|9=78|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:47:16.282|58=bye from emulator|10=068|","injected":false}]}
    interop_test.go:87: $ orderecho connect --session emu42  -> exit 0
        OrderEcho agent 0.2.0 (a2) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:50762
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu42.json (next_out=4 next_in=4)
          evidence file  : data/evidence/20260929-074716.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:47:16.230 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:50762 evidence=data/evidence/20260929-074716.jsonl
        20260929-07:47:16.230 INFO    session  emu42  Connecting to 127.0.0.1:50762
        20260929-07:47:16.230 INFO    session  emu42  Connected to 127.0.0.1:50762 (local 127.0.0.1:50941)
        20260929-07:47:16.273 INFO    session  emu42  connected: sending Logon
        20260929-07:47:16.273 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-07:47:16.273 INFO    session  emu42  store archived: outbound message store archived to data/msgstore/emu42.jsonl.20260929-074716 (Logon will carry 141=Y)
        20260929-07:47:16.274 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:47:16.273|98=0|108=30|141=Y|10=074|
        20260929-07:47:16.274 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-07:47:16.277 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:47:16.277|98=0|108=30|141=Y|10=078|
        20260929-07:47:16.282 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:47:16.282 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-07:47:16.282 IN   seq=2    35=5  8=FIX.4.2|9=78|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:47:16.282|58=bye from emulator|10=068|
        20260929-07:47:16.293 INFO    session  emu42  logout received: bye from emulator
        20260929-07:47:16.293 OUT  seq=2    35=5  8=FIX.4.2|9=80|35=5|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:47:16.293|58=Logout acknowledged|10=036|
        20260929-07:47:16.293 INFO    session  emu42  Disconnecting: Logout requested by counterparty
        20260929-07:47:16.293 INFO    session  emu42  State ACTIVE -> DISCONNECTED
        20260929-07:47:16.297 INFO    session  emu42  disconnected: next_out=3 next_in=3
        20260929-07:47:16.297 INFO    session  emu42  Connection closed, peer=127.0.0.1:50762
        20260929-07:47:16.297 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by counterparty) (their 58: "bye from emulator")
--- PASS: TestScenario1LogonTestRequestLogout (20.57s)
=== RUN   TestScenario2EmulatorTestRequest
    interop_test.go:98: emulator up: fix=50944 strict=50945 api=50946 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario2EmulatorTestRequest3401100601/001/emulator
    interop_test.go:101: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":2,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:47:33.442|112=TEST-1|10=100|","injected":false}]}
    interop_test.go:114: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
--- PASS: TestScenario2EmulatorTestRequest (17.21s)
=== RUN   TestScenario3EmulatorSeqGap
    interop_test.go:120: emulator up: fix=51105 strict=51106 api=51107 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario3EmulatorSeqGap3593738783/001/emulator
    interop_test.go:124: POST /sessions/agent42/inject/seq-gap -> {"skipped":3,"next_out":5}
    interop_test.go:126: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":5,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=5|52=20260929-07:47:34.437|112=TEST-1|10=108|","injected":false}]}
    interop_test.go:143: in sync: agent next_out=4 next_in=6 / emulator next_in=4 next_out=6 (ACTIVE)
    interop_test.go:144: emu42: TestRequest TEST-1 answered
    interop_test.go:145: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-2","sent":[{"seq":7,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=7|52=20260929-07:47:34.554|112=TEST-2|10=111|","injected":false}]}
    interop_test.go:150: in sync: agent next_out=6 next_in=8 / emulator next_in=6 next_out=8 (ACTIVE)
--- PASS: TestScenario3EmulatorSeqGap (1.09s)
=== RUN   TestScenario4AgentSkipOutboundSeq
    interop_test.go:156: emulator up: fix=51122 strict=51123 api=51124 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario4AgentSkipOutboundSeq160424792/001/emulator
    interop_test.go:188: in sync: agent next_out=6 next_in=3 / emulator next_in=6 next_out=3 (ACTIVE)
    interop_test.go:189: emu44: TestRequest TEST-2 answered
    interop_test.go:190: in sync: agent next_out=7 next_in=4 / emulator next_in=7 next_out=4 (ACTIVE)
    interop_test.go:201: in sync: agent next_out=8 next_in=5 / emulator next_in=8 next_out=5 (ACTIVE)
--- PASS: TestScenario4AgentSkipOutboundSeq (1.08s)
=== RUN   TestScenario5ReconnectWithoutReset
    interop_test.go:207: emulator up: fix=51137 strict=51138 api=51139 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario5ReconnectWithoutReset3767725438/001/emulator
    interop_test.go:210: emu42: TestRequest TEST-1 answered
    interop_test.go:211: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:213: POST /sessions/agent42/disconnect -> {"disconnected":true}
    interop_test.go:239: in sync: agent next_out=4 next_in=4 / emulator next_in=4 next_out=4 (ACTIVE)
    interop_test.go:240: emu42: TestRequest TEST-2 answered
    interop_test.go:241: in sync: agent next_out=5 next_in=5 / emulator next_in=5 next_out=5 (ACTIVE)
--- PASS: TestScenario5ReconnectWithoutReset (2.37s)
=== RUN   TestScenario6WrongVersion
    interop_test.go:253: emulator up: fix=51158 strict=51159 api=51160 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario6WrongVersion4169954522/001/emulator
    interop_test.go:255: $ orderecho connect --session strict44  -> exit 1
        OrderEcho agent 0.2.0 (a2) - session strict44
          config         : orderecho.yaml
          route          : AGENT -> STRICTBRK  FIX.4.4  127.0.0.1:51159
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/strict44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074738.jsonl
          fix log        : logs/fix/strict44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:47:38.967 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=strict44 FIX.4.4 AGENT->STRICTBRK@127.0.0.1:51159 evidence=data/evidence/20260929-074738.jsonl
        20260929-07:47:38.972 INFO    session  strict44  Connecting to 127.0.0.1:51159
        20260929-07:47:38.973 INFO    session  strict44  Connected to 127.0.0.1:51159 (local 127.0.0.1:51168)
        20260929-07:47:39.052 INFO    session  strict44  connected: sending Logon
        20260929-07:47:39.052 INFO    session  strict44  seqnums reset: Logon will carry 141=Y
        20260929-07:47:39.052 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-07:47:39.052|98=0|108=30|141=Y|10=105|
        20260929-07:47:39.053 INFO    session  strict44  State DISCONNECTED -> LOGON_SENT
        20260929-07:47:39.055 IN   seq=1    35=5  8=FIX.4.2|9=100|35=5|49=STRICTBRK|56=AGENT|34=1|52=20260929-07:47:39.055|58=Incorrect BeginString, expected FIX.4.2|10=116|
        20260929-07:47:39.059 WARNING session  strict44  logon refused: counterparty answered Logon with Logout: Incorrect BeginString, expected FIX.4.2
        20260929-07:47:39.059 OUT  seq=2    35=5  8=FIX.4.4|9=80|35=5|49=AGENT|56=STRICTBRK|34=2|52=20260929-07:47:39.059|58=Logout acknowledged|10=072|
        20260929-07:47:39.059 INFO    session  strict44  Disconnecting: Logon refused by counterparty: Incorrect BeginString, expected FIX.4.2
        20260929-07:47:39.063 INFO    session  strict44  disconnected: next_out=3 next_in=1
        20260929-07:47:39.063 INFO    session  strict44  State LOGON_SENT -> DISCONNECTED
        20260929-07:47:39.063 INFO    session  strict44  Connection closed, peer=127.0.0.1:51159
        20260929-07:47:39.063 INFO    session  engine  Shutdown complete
        >>> LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
        orderecho: LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
    interop_test.go:274: $ orderecho connect --session strict --duration 500ms  -> exit 0
        OrderEcho agent 0.2.0 (a2) - session strict
          config         : orderecho.yaml
          route          : AGENT -> STRICTBRK  FIX.4.2  127.0.0.1:51159
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/strict.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074739.jsonl
          fix log        : logs/fix/strict_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:47:39.107 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=strict FIX.4.2 AGENT->STRICTBRK@127.0.0.1:51159 evidence=data/evidence/20260929-074739.jsonl
        20260929-07:47:39.108 INFO    session  strict  Connecting to 127.0.0.1:51159
        20260929-07:47:39.108 INFO    session  strict  Connected to 127.0.0.1:51159 (local 127.0.0.1:51169)
        20260929-07:47:39.118 INFO    session  strict  connected: sending Logon
        20260929-07:47:39.118 INFO    session  strict  seqnums reset: Logon will carry 141=Y
        20260929-07:47:39.118 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-07:47:39.118|98=0|108=30|141=Y|10=106|
        20260929-07:47:39.119 INFO    session  strict  State DISCONNECTED -> LOGON_SENT
        20260929-07:47:39.124 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=STRICTBRK|56=AGENT|34=1|52=20260929-07:47:39.123|98=0|108=30|141=Y|10=102|
        20260929-07:47:39.128 INFO    session  strict  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:47:39.128 INFO    session  strict  State LOGON_SENT -> ACTIVE
        >>> Logged on to STRICTBRK as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        >>> Duration 500ms elapsed; logging out
        20260929-07:47:39.635 OUT  seq=2    35=5  8=FIX.4.2|9=94|35=5|49=AGENT|56=STRICTBRK|34=2|52=20260929-07:47:39.635|58=OrderEcho agent: duration elapsed|10=049|
        20260929-07:47:39.635 INFO    session  strict  logout initiated: OrderEcho agent: duration elapsed
        20260929-07:47:39.635 INFO    session  strict  State ACTIVE -> LOGOUT_SENT
        20260929-07:47:39.637 IN   seq=2    35=5  8=FIX.4.2|9=80|35=5|49=STRICTBRK|56=AGENT|34=2|52=20260929-07:47:39.637|58=Logout acknowledged|10=072|
        20260929-07:47:39.642 INFO    session  strict  logout confirmed: Logout acknowledged
        20260929-07:47:39.642 INFO    session  strict  Disconnecting: Logout confirmed
        20260929-07:47:39.646 INFO    session  strict  disconnected: next_out=3 next_in=3
        20260929-07:47:39.646 INFO    session  strict  State LOGOUT_SENT -> DISCONNECTED
        20260929-07:47:39.646 INFO    session  strict  Connection closed, peer=127.0.0.1:51159
        20260929-07:47:39.646 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
--- PASS: TestScenario6WrongVersion (1.59s)
=== RUN   TestScenario7UnknownCompIDs
    interop_test.go:282: emulator up: fix=51170 strict=51171 api=51172 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario7UnknownCompIDs659652861/001/emulator
    interop_test.go:284: $ orderecho connect --session unknown  -> exit 1
        OrderEcho agent 0.2.0 (a2) - session unknown
          config         : orderecho.yaml
          route          : NOBODY -> ORDERECHO  FIX.4.2  127.0.0.1:51170
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/unknown.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074740.jsonl
          fix log        : logs/fix/unknown_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:47:40.595 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=unknown FIX.4.2 NOBODY->ORDERECHO@127.0.0.1:51170 evidence=data/evidence/20260929-074740.jsonl
        20260929-07:47:40.596 INFO    session  unknown  Connecting to 127.0.0.1:51170
        20260929-07:47:40.596 INFO    session  unknown  Connected to 127.0.0.1:51170 (local 127.0.0.1:51181)
        20260929-07:47:40.756 INFO    session  unknown  connected: sending Logon
        20260929-07:47:40.756 INFO    session  unknown  seqnums reset: Logon will carry 141=Y
        20260929-07:47:40.756 OUT  seq=1    35=A  8=FIX.4.2|9=76|35=A|49=NOBODY|56=ORDERECHO|34=1|52=20260929-07:47:40.755|98=0|108=30|141=Y|10=169|
        20260929-07:47:40.757 INFO    session  unknown  State DISCONNECTED -> LOGON_SENT
        20260929-07:47:40.757 WARNING session  unknown  connection closed by counterparty
        20260929-07:47:40.761 INFO    session  unknown  disconnected: next_out=2 next_in=1
        20260929-07:47:40.762 INFO    session  unknown  State LOGON_SENT -> DISCONNECTED
        20260929-07:47:40.762 INFO    session  unknown  Connection closed, peer=127.0.0.1:51170
        20260929-07:47:40.762 INFO    session  engine  Shutdown complete
        >>> counterparty at 127.0.0.1:51170 closed the connection without answering our Logon (no Logout, no reason given) - check sender_comp_id=NOBODY, target_comp_id=ORDERECHO, fix_version=FIX.4.2 and the port
        orderecho: counterparty at 127.0.0.1:51170 closed the connection without answering our Logon (no Logout, no reason given) - check sender_comp_id=NOBODY, target_comp_id=ORDERECHO, fix_version=FIX.4.2 and the port
--- PASS: TestScenario7UnknownCompIDs (1.10s)
=== RUN   TestScenario8ViewerReadsAgentLogs
    interop_test.go:305: emulator up: fix=51182 strict=51183 api=51184 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario8ViewerReadsAgentLogs3134230201/001/emulator
    interop_test.go:308: emu42: TestRequest TEST-1 answered
    interop_test.go:309: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":3,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:47:41.806|112=TEST-1|10=104|","injected":false}]}
    interop_test.go:312: POST /sessions/agent42/inject/seq-gap -> {"skipped":2,"next_out":6}
    interop_test.go:313: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-2","sent":[{"seq":6,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=6|52=20260929-07:47:41.824|112=TEST-2|10=108|","injected":false}]}
    interop_test.go:319: in sync: agent next_out=9 next_in=8 / emulator next_in=9 next_out=8 (ACTIVE)
    interop_test.go:320: emu42: TestRequest TEST-3 answered
    interop_test.go:321: in sync: agent next_out=10 next_in=9 / emulator next_in=10 next_out=9 (ACTIVE)
    interop_test.go:351: viewer stats:
        17 message(s) from 1 file(s)
          first: 2026-09-29T07:47:41.716000+00:00
          last : 2026-09-29T07:47:42.032000+00:00
        
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
    interop_test.go:368: viewer view:
        20260929-07:47:41.716 <-- emu42              1 Logon                  141=Y 108=30
        20260929-07:47:41.719 --> emu42              1 Logon                  141=Y 108=30
        20260929-07:47:41.752 <-- emu42              2 Test Request           112=TEST-1
        20260929-07:47:41.754 --> emu42              2 Heartbeat              112=TEST-1
        20260929-07:47:41.806 --> emu42              3 Test Request           112=TEST-1
        20260929-07:47:41.817 <-- emu42              3 Heartbeat              112=TEST-1
        20260929-07:47:41.825 --> emu42              6 Test Request           112=TEST-2
        20260929-07:47:41.829 <-- emu42              4 Resend Request         7=4 16=0
        20260929-07:47:41.831 --> emu42              4 Sequence Reset         36=7 123=Y [POSSDUP]
        20260929-07:47:41.843 <-- emu42              5 Heartbeat              112=TEST-2
        20260929-07:47:41.907 <-- emu42              8 Test Request           112=TEST-2
        20260929-07:47:41.908 --> emu42              7 Resend Request         7=6 16=0
        20260929-07:47:41.913 <-- emu42              6 Sequence Reset         36=9 123=Y [POSSDUP]
        20260929-07:47:41.971 <-- emu42              9 Test Request           112=TEST-3
        20260929-07:47:41.973 --> emu42              8 Heartbeat              112=TEST-3
        20260929-07:47:42.030 <-- emu42             10 Logout                 58=viewer test done
        20260929-07:47:42.032 --> emu42              9 Logout                 58=Logout acknowledged
    interop_test.go:400: viewer stats on evidence:
        17 message(s) from 1 file(s)
          first: 2026-09-29T07:47:41.716000+00:00
          last : 2026-09-29T07:47:42.032000+00:00
        
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
--- PASS: TestScenario8ViewerReadsAgentLogs (2.14s)
=== RUN   TestCtrlCLogsOutCleanly
    interop_test.go:420: emulator up: fix=51204 strict=51205 api=51206 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCtrlCLogsOutCleanly1089593025/001/emulator
    interop_test.go:432: CLI output:
        OrderEcho agent 0.2.0 (a2) - session emu44
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:51204
          heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
          seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-074744.jsonl
          fix log        : logs/fix/emu44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-07:47:44.144 INFO    session  engine  Startup: version=0.2.0 build=a2 config=orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:51204 evidence=data/evidence/20260929-074744.jsonl
        20260929-07:47:44.145 INFO    session  emu44  Connecting to 127.0.0.1:51204
        20260929-07:47:44.145 INFO    session  emu44  Connected to 127.0.0.1:51204 (local 127.0.0.1:51219)
        20260929-07:47:44.332 INFO    session  emu44  connected: sending Logon
        20260929-07:47:44.333 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
        20260929-07:47:44.333 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:47:44.332|98=0|108=30|141=Y|10=073|
        20260929-07:47:44.333 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
        20260929-07:47:44.337 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:47:44.337|98=0|108=30|141=Y|10=078|
        20260929-07:47:44.345 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-07:47:44.345 INFO    session  emu44  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        
        >>> Ctrl+C: logging out (Ctrl+C again to exit at once)
        20260929-07:47:44.362 OUT  seq=2    35=5  8=FIX.4.4|9=90|35=5|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:47:44.362|58=OrderEcho agent shutting down|10=187|
        20260929-07:47:44.362 INFO    session  emu44  logout initiated: OrderEcho agent shutting down
        20260929-07:47:44.362 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
        20260929-07:47:44.364 IN   seq=2    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:47:44.364|58=Logout acknowledged|10=038|
        20260929-07:47:44.369 INFO    session  emu44  logout confirmed: Logout acknowledged
        20260929-07:47:44.369 INFO    session  emu44  Disconnecting: Logout confirmed
        20260929-07:47:44.373 INFO    session  emu44  disconnected: next_out=3 next_in=3
        20260929-07:47:44.373 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
        20260929-07:47:44.373 INFO    session  emu44  Connection closed, peer=127.0.0.1:51204
        20260929-07:47:44.373 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
--- PASS: TestCtrlCLogsOutCleanly (1.46s)
=== RUN   TestA2Scenario01FullFill
    orders_test.go:100: emulator up: fix=51225 strict=51226 api=51227 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario01FullFill1871130866/001/emulator
    orders_test.go:116: sent D IT-20260929-074745-emu428-1 buy 100 AAPL mkt 
    orders_test.go:126: sent D IT-20260929-074745-emu428-2 sell 250 CSCO lmt 99.50
    orders_test.go:128: VERDICT 1 full fill FIX.4.2 (AAPL mkt) IT-20260929-074745-emu428-1: FILLED PASS
    orders_test.go:129: VERDICT 1 full fill FIX.4.2 (CSCO lmt day) IT-20260929-074745-emu428-2: FILLED PASS
    orders_test.go:131: parity A2-1 FIX.4.2 / agent FIX log / IT-20260929-074745-emu428-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / agent evidence / IT-20260929-074745-emu428-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / emulator FIX log / IT-20260929-074745-emu428-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / emulator evidence / IT-20260929-074745-emu428-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / agent FIX log / IT-20260929-074745-emu428-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / agent evidence / IT-20260929-074745-emu428-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / emulator FIX log / IT-20260929-074745-emu428-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.2 / emulator evidence / IT-20260929-074745-emu428-2: PASS (all PASS)
    orders_test.go:100: emulator up: fix=51237 strict=51238 api=51239 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario01FullFill1871130866/004/emulator
    orders_test.go:116: sent D IT-20260929-074747-emu449-1 buy 100 AAPL mkt 
    orders_test.go:126: sent D IT-20260929-074747-emu449-2 sell 250 CSCO lmt 99.50
    orders_test.go:128: VERDICT 1 full fill FIX.4.4 (AAPL mkt) IT-20260929-074747-emu449-1: FILLED PASS
    orders_test.go:129: VERDICT 1 full fill FIX.4.4 (CSCO lmt day) IT-20260929-074747-emu449-2: FILLED PASS
    orders_test.go:131: parity A2-1 FIX.4.4 / agent FIX log / IT-20260929-074747-emu449-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / agent evidence / IT-20260929-074747-emu449-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / emulator FIX log / IT-20260929-074747-emu449-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / emulator evidence / IT-20260929-074747-emu449-1: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / agent FIX log / IT-20260929-074747-emu449-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / agent evidence / IT-20260929-074747-emu449-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / emulator FIX log / IT-20260929-074747-emu449-2: PASS (all PASS)
    orders_test.go:131: parity A2-1 FIX.4.4 / emulator evidence / IT-20260929-074747-emu449-2: PASS (all PASS)
--- PASS: TestA2Scenario01FullFill (5.43s)
=== RUN   TestA2Scenario02Partials
    orders_test.go:100: emulator up: fix=51249 strict=51250 api=51251 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario02Partials3234369152/001/emulator
    orders_test.go:138: sent D IT-20260929-074750-emu4210-1 buy 1000 EFG lmt 10.00
    orders_test.go:150: VERDICT 2 partials EFG (left working) IT-20260929-074750-emu4210-1: PARTIALLY_FILLED PASS
    orders_test.go:152: parity A2-2 / agent FIX log / IT-20260929-074750-emu4210-1: PASS (all PASS)
    orders_test.go:152: parity A2-2 / agent evidence / IT-20260929-074750-emu4210-1: PASS (all PASS)
    orders_test.go:152: parity A2-2 / emulator FIX log / IT-20260929-074750-emu4210-1: PASS (all PASS)
    orders_test.go:152: parity A2-2 / emulator evidence / IT-20260929-074750-emu4210-1: PASS (all PASS)
--- PASS: TestA2Scenario02Partials (3.60s)
=== RUN   TestA2Scenario03OddLots
    orders_test.go:100: emulator up: fix=51263 strict=51264 api=51265 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario03OddLots2240165184/001/emulator
    orders_test.go:158: sent D IT-20260929-074754-emu4411-1 buy 1000 NOK mkt 
    orders_test.go:176: VERDICT 3 odd lots NOK 1/2/3/405/589 IT-20260929-074754-emu4411-1: FILLED PASS
    orders_test.go:178: parity A2-3 / agent FIX log / IT-20260929-074754-emu4411-1: PASS (all PASS)
    orders_test.go:178: parity A2-3 / agent evidence / IT-20260929-074754-emu4411-1: PASS (all PASS)
    orders_test.go:178: parity A2-3 / emulator FIX log / IT-20260929-074754-emu4411-1: PASS (all PASS)
    orders_test.go:178: parity A2-3 / emulator evidence / IT-20260929-074754-emu4411-1: PASS (all PASS)
--- PASS: TestA2Scenario03OddLots (3.60s)
=== RUN   TestA2Scenario04HoldReplaceCancel
    orders_test.go:100: emulator up: fix=51274 strict=51275 api=51276 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario04HoldReplaceCancel2182794464/001/emulator
    orders_test.go:185: sent D IT-20260929-074757-emu4212-1 buy 500 ZWZZT lmt 10.00
    orders_test.go:210: VERDICT 4 hold/replace/cancel ZWZZT FIX.4.2 IT-20260929-074757-emu4212-1: CANCELED PASS
    orders_test.go:212: parity A2-4 FIX.4.2 / agent FIX log / IT-20260929-074757-emu4212-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.2 / agent evidence / IT-20260929-074757-emu4212-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.2 / emulator FIX log / IT-20260929-074757-emu4212-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.2 / emulator evidence / IT-20260929-074757-emu4212-1: PASS (all PASS)
    orders_test.go:100: emulator up: fix=51286 strict=51287 api=51288 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario04HoldReplaceCancel2182794464/004/emulator
    orders_test.go:185: sent D IT-20260929-074758-emu4413-1 buy 500 ZWZZT lmt 10.00
    orders_test.go:210: VERDICT 4 hold/replace/cancel ZWZZT FIX.4.4 IT-20260929-074758-emu4413-1: CANCELED PASS
    orders_test.go:212: parity A2-4 FIX.4.4 / agent FIX log / IT-20260929-074758-emu4413-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.4 / agent evidence / IT-20260929-074758-emu4413-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.4 / emulator FIX log / IT-20260929-074758-emu4413-1: PASS (all PASS)
    orders_test.go:212: parity A2-4 FIX.4.4 / emulator evidence / IT-20260929-074758-emu4413-1: PASS (all PASS)
--- PASS: TestA2Scenario04HoldReplaceCancel (2.42s)
=== RUN   TestA2Scenario05Rejects
    orders_test.go:100: emulator up: fix=51298 strict=51299 api=51300 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario05Rejects1875544888/001/emulator
    orders_test.go:220: sent D IT-20260929-074800-emu4214-1 buy 100 KO lmt 60.00
    orders_test.go:221: sent D IT-20260929-074800-emu4214-2 buy 100 AAPL lmt 500.00
    orders_test.go:230: VERDICT 5 rule reject KO IT-20260929-074800-emu4214-1: REJECTED PASS
    orders_test.go:231: VERDICT 5 band reject AAPL lmt 500 IT-20260929-074800-emu4214-2: REJECTED PASS
    orders_test.go:233: parity A2-5 / agent FIX log / IT-20260929-074800-emu4214-1: PASS (all PASS)
    orders_test.go:233: parity A2-5 / agent evidence / IT-20260929-074800-emu4214-1: PASS (all PASS)
    orders_test.go:233: parity A2-5 / emulator FIX log / IT-20260929-074800-emu4214-1: PASS (all PASS)
    orders_test.go:233: parity A2-5 / emulator evidence / IT-20260929-074800-emu4214-1: PASS (all PASS)
    orders_test.go:233: parity A2-5 / agent FIX log / IT-20260929-074800-emu4214-2: PASS (all PASS)
    orders_test.go:233: parity A2-5 / agent evidence / IT-20260929-074800-emu4214-2: PASS (all PASS)
    orders_test.go:233: parity A2-5 / emulator FIX log / IT-20260929-074800-emu4214-2: PASS (all PASS)
    orders_test.go:233: parity A2-5 / emulator evidence / IT-20260929-074800-emu4214-2: PASS (all PASS)
    orders_test.go:237: sent D IT-20260929-074800-strict15-1 buy 100 AAPL mkt 
    orders_test.go:242: VERDICT 5 strict broker rejects all IT-20260929-074800-strict15-1: REJECTED PASS
    orders_test.go:244: parity A2-5 strict / agent FIX log / IT-20260929-074800-strict15-1: PASS (all PASS)
    orders_test.go:244: parity A2-5 strict / agent evidence / IT-20260929-074800-strict15-1: PASS (all PASS)
    orders_test.go:244: parity A2-5 strict / emulator FIX log / IT-20260929-074800-strict15-1: PASS (all PASS)
    orders_test.go:244: parity A2-5 strict / emulator evidence / IT-20260929-074800-strict15-1: PASS (all PASS)
--- PASS: TestA2Scenario05Rejects (1.52s)
=== RUN   TestA2Scenario06UnsolicitedCancel
    orders_test.go:100: emulator up: fix=51312 strict=51313 api=51314 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario06UnsolicitedCancel3103293354/001/emulator
    orders_test.go:250: sent D IT-20260929-074801-emu4416-1 buy 100 HON lmt 10.00
    orders_test.go:255: VERDICT 6 unsolicited cancel HON IT-20260929-074801-emu4416-1: CANCELED PASS
    orders_test.go:257: parity A2-6 / agent FIX log / IT-20260929-074801-emu4416-1: PASS (all PASS)
    orders_test.go:257: parity A2-6 / agent evidence / IT-20260929-074801-emu4416-1: PASS (all PASS)
    orders_test.go:257: parity A2-6 / emulator FIX log / IT-20260929-074801-emu4416-1: PASS (all PASS)
    orders_test.go:257: parity A2-6 / emulator evidence / IT-20260929-074801-emu4416-1: PASS (all PASS)
--- PASS: TestA2Scenario06UnsolicitedCancel (1.70s)
=== RUN   TestA2Scenario07ManualFills
    orders_test.go:100: emulator up: fix=51324 strict=51325 api=51326 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario07ManualFills233212492/001/emulator
    orders_test.go:263: sent D IT-20260929-074803-emu4217-1 buy 1000 ZWZZT lmt 10.00
    orders_test.go:266: POST /orders/O-20260929-074803-1/fill -> {"session":"agent42","order":{"order_id":"O-20260929-074803-1","cl_ord_id":"IT-20260929-074803-emu4217-1","symbol":"ZWZZT","side":"1","order_qty":"1000","cum_qty":"300","leaves_qty":"700","avg_px":"10.0000","ord_status":"1","price_source":"limit","rule_name":"nasdaq-test-hold"},"sent":[{"seq":3,"msg_type":"8","raw":"8=FIX.4.2|9=253|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:48:03.549|37=O-20260929-074803-1|11=IT-20260929-074803-emu4217-1|17=E-20260929-074803-2|20=0|150=1|39=1|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=300|31=10.00|151=700|14=300|6=10.0000|60=20260929-07:48:03.548|10=004|","injected":false}]}
    orders_test.go:268: POST /orders/O-20260929-074803-1/fill -> {"session":"agent42","order":{"order_id":"O-20260929-074803-1","cl_ord_id":"IT-20260929-074803-emu4217-1","symbol":"ZWZZT","side":"1","order_qty":"1000","cum_qty":"500","leaves_qty":"500","avg_px":"10.4000","ord_status":"1","price_source":"limit","rule_name":"nasdaq-test-hold"},"sent":[{"seq":4,"msg_type":"8","raw":"8=FIX.4.2|9=253|35=8|49=ORDERECHO|56=AGENT|34=4|52=20260929-07:48:03.607|37=O-20260929-074803-1|11=IT-20260929-074803-emu4217-1|17=E-20260929-074803-3|20=0|150=1|39=1|55=ZWZZT|54=1|38=1000|40=2|44=10.00|32=200|31=11.00|151=500|14=500|6=10.4000|60=20260929-07:48:03.605|10=255|","injected":false}]}
    orders_test.go:277: VERDICT 7 manual fills 300@10 + 200@11 IT-20260929-074803-emu4217-1: PARTIALLY_FILLED PASS
    orders_test.go:279: parity A2-7 / agent FIX log / IT-20260929-074803-emu4217-1: PASS (all PASS)
    orders_test.go:279: parity A2-7 / agent evidence / IT-20260929-074803-emu4217-1: PASS (all PASS)
    orders_test.go:279: parity A2-7 / emulator FIX log / IT-20260929-074803-emu4217-1: PASS (all PASS)
    orders_test.go:279: parity A2-7 / emulator evidence / IT-20260929-074803-emu4217-1: PASS (all PASS)
--- PASS: TestA2Scenario07ManualFills (1.08s)
=== RUN   TestA2Scenario08UnknownTagTolerated
    orders_test.go:100: emulator up: fix=51337 strict=51338 api=51339 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario08UnknownTagTolerated3599730706/001/emulator
    orders_test.go:285: POST /sessions/agent44/inject/next -> {"queued":{"id":1,"msg_type":"8","set":{"9999":"FOO"},"remove":[],"corrupt_checksum":false,"count":1,"remaining":1}}
    orders_test.go:286: sent D IT-20260929-074804-emu4418-1 buy 100 AAPL mkt 
    orders_test.go:303: VERDICT 8 ER with 9999=FOO IT-20260929-074804-emu4418-1: FILLED PASS
    orders_test.go:305: parity A2-8 / agent FIX log / IT-20260929-074804-emu4418-1: PASS (all PASS)
    orders_test.go:305: parity A2-8 / agent evidence / IT-20260929-074804-emu4418-1: PASS (all PASS)
    orders_test.go:305: parity A2-8 / emulator FIX log / IT-20260929-074804-emu4418-1: PASS (all PASS)
    orders_test.go:305: parity A2-8 / emulator evidence / IT-20260929-074804-emu4418-1: PASS (all PASS)
--- PASS: TestA2Scenario08UnknownTagTolerated (1.57s)
=== RUN   TestA2Scenario09DuplicateClOrdID
    orders_test.go:100: emulator up: fix=51349 strict=51350 api=51351 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario09DuplicateClOrdID3806062285/001/emulator
    orders_test.go:311: sent D IT-20260929-074806-emu4219-1 buy 500 ZWZZT lmt 10.00
    orders_test.go:344: VERDICT 9 duplicate ClOrdID via SendRaw IT-20260929-074806-emu4219-1: NEW FAIL
    orders_test.go:346: parity A2-9 / agent FIX log / IT-20260929-074806-emu4219-1: FAIL (order_id_constant=FAIL)
    orders_test.go:346: parity A2-9 / agent evidence / IT-20260929-074806-emu4219-1: FAIL (order_id_constant=FAIL)
    orders_test.go:346: parity A2-9 / emulator FIX log / IT-20260929-074806-emu4219-1: FAIL (order_id_constant=FAIL)
    orders_test.go:346: parity A2-9 / emulator evidence / IT-20260929-074806-emu4219-1: FAIL (order_id_constant=FAIL)
--- PASS: TestA2Scenario09DuplicateClOrdID (1.17s)
=== RUN   TestA2Scenario10TestRequestBehindGap
    orders_test.go:100: emulator up: fix=51360 strict=51361 api=51362 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario10TestRequestBehindGap3706905266/001/emulator
    orders_test.go:352: POST /sessions/agent42/inject/seq-gap -> {"skipped":3,"next_out":5}
    orders_test.go:353: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":5,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=5|52=20260929-07:48:07.495|112=TEST-1|10=113|","injected":false}]}
    orders_test.go:376: in sync: agent next_out=4 next_in=6 / emulator next_in=4 next_out=6 (ACTIVE)
    orders_test.go:377: sent D IT-20260929-074807-emu4220-1 buy 100 AAPL mkt 
    orders_test.go:379: VERDICT 10 TestRequest behind gap, then AAPL IT-20260929-074807-emu4220-1: FILLED PASS
    orders_test.go:381: parity A2-10 / agent FIX log / IT-20260929-074807-emu4220-1: PASS (all PASS)
    orders_test.go:381: parity A2-10 / agent evidence / IT-20260929-074807-emu4220-1: PASS (all PASS)
    orders_test.go:381: parity A2-10 / emulator FIX log / IT-20260929-074807-emu4220-1: PASS (all PASS)
    orders_test.go:381: parity A2-10 / emulator evidence / IT-20260929-074807-emu4220-1: PASS (all PASS)
--- PASS: TestA2Scenario10TestRequestBehindGap (1.81s)
=== RUN   TestA2Scenario11CancelUnknown
    orders_test.go:100: emulator up: fix=51377 strict=51378 api=51379 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestA2Scenario11CancelUnknown316461106/001/emulator
    orders_test.go:414: parity A2-11 / agent FIX log / RAWF-dlrmuztmlkdc: PASS (all PASS)
    orders_test.go:414: parity A2-11 / agent evidence / RAWF-dlrmuztmlkdc: PASS (all PASS)
    orders_test.go:414: parity A2-11 / emulator FIX log / RAWF-dlrmuztmlkdc: PASS (all PASS)
    orders_test.go:414: parity A2-11 / emulator evidence / RAWF-dlrmuztmlkdc: PASS (all PASS)
--- PASS: TestA2Scenario11CancelUnknown (1.26s)
=== RUN   TestParityTampered
    orders_test.go:100: emulator up: fix=51389 strict=51390 api=51391 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestParityTampered1591601989/001/emulator
    parity_test.go:104: sent D IT-20260929-074810-emu4422-1 buy 1000 NOK mkt 
    parity_test.go:105: sent D IT-20260929-074810-emu4422-2 buy 500 ZWZZT lmt 10.00
    parity_test.go:117: VERDICT tamper base: NOK odd lots (4.4) IT-20260929-074810-emu4422-1: FILLED PASS
    parity_test.go:118: VERDICT tamper base: ZWZZT replace/cancel (4.4) IT-20260929-074810-emu4422-2: CANCELED PASS
    parity_test.go:260: parity tampered agent FIX log: cum_qty_monotonic (IT-20260929-074810-emu4422-1): FAIL (cum_qty_monotonic=FAIL working_quantities=FAIL)
    parity_test.go:260: parity tampered agent FIX log: working_quantities (IT-20260929-074810-emu4422-1): FAIL (working_quantities=FAIL)
    parity_test.go:260: parity tampered agent FIX log: terminal_quantities (IT-20260929-074810-emu4422-1): FAIL (terminal_quantities=FAIL)
    parity_test.go:260: parity tampered agent FIX log: fill_quantities_sum (IT-20260929-074810-emu4422-1): FAIL (fill_quantities_sum=FAIL)
    parity_test.go:260: parity tampered agent FIX log: avg_px (IT-20260929-074810-emu4422-1): FAIL (avg_px=FAIL)
    parity_test.go:260: parity tampered agent FIX log: exec_ids_unique (IT-20260929-074810-emu4422-1): FAIL (exec_ids_unique=FAIL)
    parity_test.go:260: parity tampered agent FIX log: order_id_constant (IT-20260929-074810-emu4422-1): FAIL (order_id_constant=FAIL)
    parity_test.go:260: parity tampered agent FIX log: nothing_after_terminal (IT-20260929-074810-emu4422-1): FAIL (fill_quantities_sum=FAIL nothing_after_terminal=FAIL)
    parity_test.go:260: parity tampered agent FIX log: version_rules (IT-20260929-074810-emu4422-1): FAIL (version_rules=FAIL)
    parity_test.go:260: parity tampered agent FIX log: version_rules (IT-20260929-074810-emu4422-2): FAIL (version_rules=FAIL)
    parity_test.go:260: parity tampered agent FIX log: requests_answered (IT-20260929-074810-emu4422-2): WARN (requests_answered=WARN)
    parity_test.go:260: parity tampered agent FIX log: framing_intact (IT-20260929-074810-emu4422-2): WARN (framing_intact=WARN)
    parity_test.go:260: parity tampered agent evidence: avg_px (IT-20260929-074810-emu4422-1): FAIL (avg_px=FAIL)
--- PASS: TestParityTampered (3.62s)
=== RUN   TestParityKnownDivergences
    parity_test.go:353: divergence 1: own Reject counted as an answer                          go=WARN py=PASS [requests_answered: go WARN, py PASS]
    parity_test.go:353: divergence 2: tag 110= truncates the message                           go=PASS py=WARN [framing_intact: go PASS, py WARN]
    parity_test.go:353: divergence 3: non-ASCII value breaks the checksum                      go=PASS py=WARN [framing_intact: go PASS, py WARN]
--- PASS: TestParityKnownDivergences (0.06s)
PASS

PARITY SUMMARY: 85 case(s) compared, 85 agree, 0 disagree
  AGREE    CLI session / agent FIX log / OE-20260929-074653-1                     go=PASS py=PASS [all PASS]
  AGREE    CLI session / emulator FIX log / OE-20260929-074653-1                  go=PASS py=PASS [all PASS]
  AGREE    CLI session / agent FIX log / OE-20260929-074653-2                     go=PASS py=PASS [all PASS]
  AGREE    CLI session / emulator FIX log / OE-20260929-074653-2                  go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent FIX log / IT-20260929-074745-emu428-1             go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent evidence / IT-20260929-074745-emu428-1            go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator FIX log / IT-20260929-074745-emu428-1          go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator evidence / IT-20260929-074745-emu428-1         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent FIX log / IT-20260929-074745-emu428-2             go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / agent evidence / IT-20260929-074745-emu428-2            go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator FIX log / IT-20260929-074745-emu428-2          go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.2 / emulator evidence / IT-20260929-074745-emu428-2         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent FIX log / IT-20260929-074747-emu449-1             go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent evidence / IT-20260929-074747-emu449-1            go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator FIX log / IT-20260929-074747-emu449-1          go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator evidence / IT-20260929-074747-emu449-1         go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent FIX log / IT-20260929-074747-emu449-2             go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / agent evidence / IT-20260929-074747-emu449-2            go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator FIX log / IT-20260929-074747-emu449-2          go=PASS py=PASS [all PASS]
  AGREE    A2-1 FIX.4.4 / emulator evidence / IT-20260929-074747-emu449-2         go=PASS py=PASS [all PASS]
  AGREE    A2-2 / agent FIX log / IT-20260929-074750-emu4210-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-2 / agent evidence / IT-20260929-074750-emu4210-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-2 / emulator FIX log / IT-20260929-074750-emu4210-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-2 / emulator evidence / IT-20260929-074750-emu4210-1                go=PASS py=PASS [all PASS]
  AGREE    A2-3 / agent FIX log / IT-20260929-074754-emu4411-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-3 / agent evidence / IT-20260929-074754-emu4411-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-3 / emulator FIX log / IT-20260929-074754-emu4411-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-3 / emulator evidence / IT-20260929-074754-emu4411-1                go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / agent FIX log / IT-20260929-074757-emu4212-1            go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / agent evidence / IT-20260929-074757-emu4212-1           go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / emulator FIX log / IT-20260929-074757-emu4212-1         go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.2 / emulator evidence / IT-20260929-074757-emu4212-1        go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / agent FIX log / IT-20260929-074758-emu4413-1            go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / agent evidence / IT-20260929-074758-emu4413-1           go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / emulator FIX log / IT-20260929-074758-emu4413-1         go=PASS py=PASS [all PASS]
  AGREE    A2-4 FIX.4.4 / emulator evidence / IT-20260929-074758-emu4413-1        go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent FIX log / IT-20260929-074800-emu4214-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent evidence / IT-20260929-074800-emu4214-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator FIX log / IT-20260929-074800-emu4214-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator evidence / IT-20260929-074800-emu4214-1                go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent FIX log / IT-20260929-074800-emu4214-2                    go=PASS py=PASS [all PASS]
  AGREE    A2-5 / agent evidence / IT-20260929-074800-emu4214-2                   go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator FIX log / IT-20260929-074800-emu4214-2                 go=PASS py=PASS [all PASS]
  AGREE    A2-5 / emulator evidence / IT-20260929-074800-emu4214-2                go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / agent FIX log / IT-20260929-074800-strict15-1            go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / agent evidence / IT-20260929-074800-strict15-1           go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / emulator FIX log / IT-20260929-074800-strict15-1         go=PASS py=PASS [all PASS]
  AGREE    A2-5 strict / emulator evidence / IT-20260929-074800-strict15-1        go=PASS py=PASS [all PASS]
  AGREE    A2-6 / agent FIX log / IT-20260929-074801-emu4416-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-6 / agent evidence / IT-20260929-074801-emu4416-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-6 / emulator FIX log / IT-20260929-074801-emu4416-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-6 / emulator evidence / IT-20260929-074801-emu4416-1                go=PASS py=PASS [all PASS]
  AGREE    A2-7 / agent FIX log / IT-20260929-074803-emu4217-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-7 / agent evidence / IT-20260929-074803-emu4217-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-7 / emulator FIX log / IT-20260929-074803-emu4217-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-7 / emulator evidence / IT-20260929-074803-emu4217-1                go=PASS py=PASS [all PASS]
  AGREE    A2-8 / agent FIX log / IT-20260929-074804-emu4418-1                    go=PASS py=PASS [all PASS]
  AGREE    A2-8 / agent evidence / IT-20260929-074804-emu4418-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-8 / emulator FIX log / IT-20260929-074804-emu4418-1                 go=PASS py=PASS [all PASS]
  AGREE    A2-8 / emulator evidence / IT-20260929-074804-emu4418-1                go=PASS py=PASS [all PASS]
  AGREE    A2-9 / agent FIX log / IT-20260929-074806-emu4219-1                    go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    A2-9 / agent evidence / IT-20260929-074806-emu4219-1                   go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    A2-9 / emulator FIX log / IT-20260929-074806-emu4219-1                 go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    A2-9 / emulator evidence / IT-20260929-074806-emu4219-1                go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    A2-10 / agent FIX log / IT-20260929-074807-emu4220-1                   go=PASS py=PASS [all PASS]
  AGREE    A2-10 / agent evidence / IT-20260929-074807-emu4220-1                  go=PASS py=PASS [all PASS]
  AGREE    A2-10 / emulator FIX log / IT-20260929-074807-emu4220-1                go=PASS py=PASS [all PASS]
  AGREE    A2-10 / emulator evidence / IT-20260929-074807-emu4220-1               go=PASS py=PASS [all PASS]
  AGREE    A2-11 / agent FIX log / RAWF-dlrmuztmlkdc                              go=PASS py=PASS [all PASS]
  AGREE    A2-11 / agent evidence / RAWF-dlrmuztmlkdc                             go=PASS py=PASS [all PASS]
  AGREE    A2-11 / emulator FIX log / RAWF-dlrmuztmlkdc                           go=PASS py=PASS [all PASS]
  AGREE    A2-11 / emulator evidence / RAWF-dlrmuztmlkdc                          go=PASS py=PASS [all PASS]
  AGREE    tampered agent FIX log: cum_qty_monotonic (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [cum_qty_monotonic=FAIL working_quantities=FAIL]
  AGREE    tampered agent FIX log: working_quantities (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [working_quantities=FAIL]
  AGREE    tampered agent FIX log: terminal_quantities (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [terminal_quantities=FAIL]
  AGREE    tampered agent FIX log: fill_quantities_sum (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [fill_quantities_sum=FAIL]
  AGREE    tampered agent FIX log: avg_px (IT-20260929-074810-emu4422-1)          go=FAIL py=FAIL [avg_px=FAIL]
  AGREE    tampered agent FIX log: exec_ids_unique (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [exec_ids_unique=FAIL]
  AGREE    tampered agent FIX log: order_id_constant (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [order_id_constant=FAIL]
  AGREE    tampered agent FIX log: nothing_after_terminal (IT-20260929-074810-emu4422-1) go=FAIL py=FAIL [fill_quantities_sum=FAIL nothing_after_terminal=FAIL]
  AGREE    tampered agent FIX log: version_rules (IT-20260929-074810-emu4422-1)   go=FAIL py=FAIL [version_rules=FAIL]
  AGREE    tampered agent FIX log: version_rules (IT-20260929-074810-emu4422-2)   go=FAIL py=FAIL [version_rules=FAIL]
  AGREE    tampered agent FIX log: requests_answered (IT-20260929-074810-emu4422-2) go=WARN py=WARN [requests_answered=WARN]
  AGREE    tampered agent FIX log: framing_intact (IT-20260929-074810-emu4422-2)  go=WARN py=WARN [framing_intact=WARN]
  AGREE    tampered agent evidence: avg_px (IT-20260929-074810-emu4422-1)         go=FAIL py=FAIL [avg_px=FAIL]
  DIVERGE  divergence 1: own Reject counted as an answer                          go=WARN py=PASS [requests_answered: go WARN, py PASS] (documented, expected)
  DIVERGE  divergence 2: tag 110= truncates the message                           go=PASS py=WARN [framing_intact: go PASS, py WARN] (documented, expected)
  DIVERGE  divergence 3: non-ASCII value breaks the checksum                      go=PASS py=WARN [framing_intact: go PASS, py WARN] (documented, expected)

SCENARIO VERDICTS (Go, live):
  1 full fill FIX.4.2 (AAPL mkt)               IT-20260929-074745-emu428-1    FILLED            PASS 
  1 full fill FIX.4.2 (CSCO lmt day)           IT-20260929-074745-emu428-2    FILLED            PASS 
  1 full fill FIX.4.4 (AAPL mkt)               IT-20260929-074747-emu449-1    FILLED            PASS 
  1 full fill FIX.4.4 (CSCO lmt day)           IT-20260929-074747-emu449-2    FILLED            PASS 
  2 partials EFG (left working)                IT-20260929-074750-emu4210-1   PARTIALLY_FILLED  PASS 
  3 odd lots NOK 1/2/3/405/589                 IT-20260929-074754-emu4411-1   FILLED            PASS 
  4 hold/replace/cancel ZWZZT FIX.4.2          IT-20260929-074757-emu4212-1   CANCELED          PASS 
  4 hold/replace/cancel ZWZZT FIX.4.4          IT-20260929-074758-emu4413-1   CANCELED          PASS 
  5 rule reject KO                             IT-20260929-074800-emu4214-1   REJECTED          PASS 
  5 band reject AAPL lmt 500                   IT-20260929-074800-emu4214-2   REJECTED          PASS 
  5 strict broker rejects all                  IT-20260929-074800-strict15-1  REJECTED          PASS 
  6 unsolicited cancel HON                     IT-20260929-074801-emu4416-1   CANCELED          PASS 
  7 manual fills 300@10 + 200@11               IT-20260929-074803-emu4217-1   PARTIALLY_FILLED  PASS 
  8 ER with 9999=FOO                           IT-20260929-074804-emu4418-1   FILLED            PASS 
  9 duplicate ClOrdID via SendRaw              IT-20260929-074806-emu4219-1   NEW               FAIL order_id_constant=FAIL (the chain carries 2 OrderIDs: O-20260929-074805-1, O-20260929-074805-2)
  10 TestRequest behind gap, then AAPL         IT-20260929-074807-emu4220-1   FILLED            PASS 
  11 cancel unknown ClOrdID via SendRaw        RAWF-dlrmuztmlkdc              (no order)        PASS
  tamper base: NOK odd lots (4.4)              IT-20260929-074810-emu4422-1   FILLED            PASS 
  tamper base: ZWZZT replace/cancel (4.4)      IT-20260929-074810-emu4422-2   CANCELED          PASS 
ok  	github.com/danielgavin-code/OrderEcho/internal/interop	117.044s
```
</details>

## 3. Real run

### 3.1 The emulator was already running — yours

When I started the emulator with `config/orderecho_multi.yaml`, it failed with `address already in use`. `lsof` showed PID 58696 already listening on 9878, 9879 and 8090. That process is `python orderecho_Main.py --config config/orderecho_multi.yaml`, started at 02:23 local from `../OrderEchoFixEmulator`, with a terminal shell as its parent. I took that to mean you started it.

It is exactly the emulator the spec asks for, so the two runs below went to it. That has two consequences:
- **I did not stop it**, because it isn't mine. It is still running.
- **Its runtime files now include my traffic:** 14 lines in `logs/fix/agent42_20260929.log`, 7 in `agent44_20260929.log`, plus the matching lines in its engine log, evidence file `data/evidence/20260929-062406.jsonl`, seqnums and msgstore.

No emulator source or config file was touched. Everything else I started has exited: my own emulator never bound, and both CLI runs finished.

### 3.2 `bin/orderecho order --session emu44 AAPL 100 buy mkt` (live pricing)
```
OrderEcho agent 0.2.0 (a2) - session emu44
  config         : config/orderecho.yaml
  route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:9878
  heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
  seqnums        : data/seqnums/emu44.json (next_out=4 next_in=7)
  evidence file  : data/evidence/20260929-074939.jsonl
  fix log        : logs/fix/emu44_20260929.log
  engine log     : logs/engine/orderecho_20260929.log
  Ctrl+C to log out; Ctrl+C again to exit at once.
20260929-07:49:39.932 INFO    session  engine  Startup: version=0.2.0 build=a2 config=config/orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:9878 evidence=data/evidence/20260929-074939.jsonl
20260929-07:49:39.992 INFO    session  emu44  Connecting to 127.0.0.1:9878
20260929-07:49:39.993 INFO    session  emu44  Connected to 127.0.0.1:9878 (local 127.0.0.1:51407)
20260929-07:49:40.272 INFO    session  emu44  connected: sending Logon
20260929-07:49:40.272 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
20260929-07:49:40.272 INFO    session  emu44  store archived: outbound message store archived to data/msgstore/emu44.jsonl.20260929-074940 (Logon will carry 141=Y)
20260929-07:49:40.272 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:49:40.271|98=0|108=30|141=Y|10=073|
20260929-07:49:40.310 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
20260929-07:49:40.367 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:49:40.366|98=0|108=30|141=Y|10=078|
20260929-07:49:40.441 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
20260929-07:49:40.441 INFO    session  emu44  State LOGON_SENT -> ACTIVE
>>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
20260929-07:49:40.450 OUT  seq=2    35=D  8=FIX.4.4|9=136|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:49:40.450|11=OE-20260929-074939-1|21=1|55=AAPL|54=1|60=20260929-07:49:40.441|38=100|40=1|10=093|
>> D 11=OE-20260929-074939-1 BUY 100 AAPL MKT sent
20260929-07:49:40.454 IN   seq=2    35=8  8=FIX.4.4|9=223|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:49:40.454|37=O-20260929-062406-1|11=OE-20260929-074939-1|17=E-20260929-062406-1|150=0|39=0|55=AAPL|54=1|38=100|40=1|32=0|31=0.00|151=100|14=0|6=0.0000|60=20260929-07:49:40.452|10=080|
20260929-07:49:40.459 INFO    session  emu44  application message received: 35=8 (ExecutionReport) seq=2
20260929-07:49:40.459 INFO    session  emu44  << ER 11=OE-20260929-074939-1 37=O-20260929-062406-1 150=0(New) 39=0(New) cum=0 leaves=100 avg=0.0000  -> NEW cum=0 leaves=100  checks: PASS
20260929-07:49:48.323 IN   seq=3    35=8  8=FIX.4.4|9=229|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:49:48.323|37=O-20260929-062406-1|11=OE-20260929-074939-1|17=E-20260929-062406-2|150=F|39=2|55=AAPL|54=1|38=100|40=1|32=100|31=338.40|151=0|14=100|6=338.4000|60=20260929-07:49:48.322|10=188|
20260929-07:49:48.616 INFO    session  emu44  application message received: 35=8 (ExecutionReport) seq=3
20260929-07:49:48.616 INFO    session  emu44  << ER 11=OE-20260929-074939-1 37=O-20260929-062406-1 150=F(Trade) 39=2(Filled) last=100@338.40 cum=100 leaves=0 avg=338.4000  -> FILLED cum=100 leaves=0  checks: PASS

Order chain for OE-20260929-074939-1
  ClOrdIDs: OE-20260929-074939-1
  OrderID : O-20260929-062406-1

  time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
  2026-09-29T07:49:40.450 <--  NewOrderSingle       - / -                            100              -       -       -          -
  2026-09-29T07:49:40.459 -->  ExecutionReport      0 (New) / 0 (New)                100              -       0     100     0.0000
  2026-09-29T07:49:48.616 -->  ExecutionReport      F (Trade) / 2 (Filled)           100     100@338.40     100       0   338.4000

Checks
  [PASS] cum_qty_monotonic: CumQty rose to 100 without ever falling
  [PASS] working_quantities: 1 working report(s) balanced
  [PASS] terminal_quantities: 1 terminal report(s) consistent
  [PASS] fill_quantities_sum: 1 fill(s) totalling 100 match CumQty
  [PASS] avg_px: AvgPx 338.4000 matches the fills to within 0.0001
  [PASS] exec_ids_unique: 2 ExecID(s), all distinct
  [PASS] order_id_constant: OrderID O-20260929-062406-1 throughout
  [PASS] nothing_after_terminal: terminal 39=2 was the last word
  [PASS] version_rules: every report matches its version's conventions
  [PASS] requests_answered: all 1 request(s) answered
  [PASS] framing_intact: all 3 message(s) correctly framed

  verdict: PASS
20260929-07:49:48.679 OUT  seq=3    35=5  8=FIX.4.4|9=88|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-07:49:48.679|58=OrderEcho agent: order done|10=162|
20260929-07:49:48.679 INFO    session  emu44  logout initiated: OrderEcho agent: order done
20260929-07:49:48.679 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
20260929-07:49:48.681 IN   seq=4    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=4|52=20260929-07:49:48.681|58=Logout acknowledged|10=048|
20260929-07:49:48.687 INFO    session  emu44  logout confirmed: Logout acknowledged
20260929-07:49:48.687 INFO    session  emu44  Disconnecting: Logout confirmed
20260929-07:49:48.691 INFO    session  emu44  disconnected: next_out=4 next_in=5
20260929-07:49:48.691 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
20260929-07:49:48.691 INFO    session  emu44  Connection closed, peer=127.0.0.1:9878
20260929-07:49:48.691 INFO    session  engine  Shutdown complete
>>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
exit 0
```

### 3.3 Piped `bin/orderecho session --session emu42`

The input, piped in:
```
order EFG 1000 buy lmt 10.00
order ZWZZT 500 buy lmt 10.00
replace last 800 10.50
cancel last
status
timeline last
quit
```
The output:
```
OrderEcho agent 0.2.0 (a2) - session emu42
  config         : config/orderecho.yaml
  route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:9878
  heartbeat      : 30s  reset_on_logon=true  reconnect=false  heartbeat_mismatch=warn
  seqnums        : data/seqnums/emu42.json (next_out=4 next_in=4)
  evidence file  : data/evidence/20260929-075023.jsonl
  fix log        : logs/fix/emu42_20260929.log
  engine log     : logs/engine/orderecho_20260929.log
  Ctrl+C to log out; Ctrl+C again to exit at once.
20260929-07:50:23.304 INFO    session  engine  Startup: version=0.2.0 build=a2 config=config/orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:9878 evidence=data/evidence/20260929-075023.jsonl
20260929-07:50:23.304 INFO    session  emu42  Connecting to 127.0.0.1:9878
20260929-07:50:23.305 INFO    session  emu42  Connected to 127.0.0.1:9878 (local 127.0.0.1:51412)
20260929-07:50:23.470 INFO    session  emu42  connected: sending Logon
20260929-07:50:23.470 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
20260929-07:50:23.470 INFO    session  emu42  store archived: outbound message store archived to data/msgstore/emu42.jsonl.20260929-075023 (Logon will carry 141=Y)
20260929-07:50:23.470 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-07:50:23.469|98=0|108=30|141=Y|10=073|
20260929-07:50:23.589 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
20260929-07:50:23.589 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-07:50:23.586|98=0|108=30|141=Y|10=073|
20260929-07:50:23.598 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
20260929-07:50:23.598 INFO    session  emu42  State LOGON_SENT -> ACTIVE
>>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
>>> session ready; type "help" for commands
> order EFG 1000 buy lmt 10.00
20260929-07:50:23.602 OUT  seq=2    35=D  8=FIX.4.2|9=145|35=D|49=AGENT|56=ORDERECHO|34=2|52=20260929-07:50:23.602|11=OE-20260929-075023-1|21=1|55=EFG|54=1|60=20260929-07:50:23.598|38=1000|40=2|44=10.00|10=196|
>> D 11=OE-20260929-075023-1 BUY 1000 EFG LMT 10.00 sent
20260929-07:50:24.273 IN   seq=2    35=8  8=FIX.4.2|9=238|35=8|49=ORDERECHO|56=AGENT|34=2|52=20260929-07:50:24.273|37=O-20260929-062406-2|11=OE-20260929-075023-1|17=E-20260929-062406-3|20=0|150=0|39=0|55=EFG|54=1|38=1000|40=2|44=10.00|32=0|31=0.00|151=1000|14=0|6=0.0000|60=20260929-07:50:24.272|10=181|
20260929-07:50:24.322 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=2
20260929-07:50:24.322 INFO    session  emu42  << ER 11=OE-20260929-075023-1 37=O-20260929-062406-2 150=0(New) 39=0(New) cum=0 leaves=1000 avg=0.0000  -> NEW cum=0 leaves=1000  checks: PASS
> order ZWZZT 500 buy lmt 10.00
20260929-07:50:24.378 OUT  seq=3    35=D  8=FIX.4.2|9=146|35=D|49=AGENT|56=ORDERECHO|34=3|52=20260929-07:50:24.378|11=OE-20260929-075023-2|21=1|55=ZWZZT|54=1|60=20260929-07:50:24.373|38=500|40=2|44=10.00|10=133|
>> D 11=OE-20260929-075023-2 BUY 500 ZWZZT LMT 10.00 sent
20260929-07:50:24.806 IN   seq=3    35=8  8=FIX.4.2|9=243|35=8|49=ORDERECHO|56=AGENT|34=3|52=20260929-07:50:24.805|37=O-20260929-062406-2|11=OE-20260929-075023-1|17=E-20260929-062406-4|20=0|150=1|39=1|55=EFG|54=1|38=1000|40=2|44=10.00|32=400|31=10.00|151=600|14=400|6=10.0000|60=20260929-07:50:24.804|10=182|
20260929-07:50:24.816 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=3
20260929-07:50:24.816 INFO    session  emu42  << ER 11=OE-20260929-075023-1 37=O-20260929-062406-2 150=1(Partial fill) 39=1(Partially filled) last=400@10.00 cum=400 leaves=600 avg=10.0000  -> PARTIALLY_FILLED cum=400 leaves=600  checks: PASS
20260929-07:50:25.849 IN   seq=4    35=8  8=FIX.4.2|9=243|35=8|49=ORDERECHO|56=AGENT|34=4|52=20260929-07:50:25.848|37=O-20260929-062406-2|11=OE-20260929-075023-1|17=E-20260929-062406-5|20=0|150=1|39=1|55=EFG|54=1|38=1000|40=2|44=10.00|32=100|31=10.00|151=500|14=500|6=10.0000|60=20260929-07:50:25.847|10=197|
20260929-07:50:25.923 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=4
20260929-07:50:25.923 INFO    session  emu42  << ER 11=OE-20260929-075023-1 37=O-20260929-062406-2 150=1(Partial fill) 39=1(Partially filled) last=100@10.00 cum=500 leaves=500 avg=10.0000  -> PARTIALLY_FILLED cum=500 leaves=500  checks: PASS
20260929-07:50:25.923 IN   seq=5    35=8  8=FIX.4.2|9=238|35=8|49=ORDERECHO|56=AGENT|34=5|52=20260929-07:50:25.853|37=O-20260929-062406-3|11=OE-20260929-075023-2|17=E-20260929-062406-6|20=0|150=0|39=0|55=ZWZZT|54=1|38=500|40=2|44=10.00|32=0|31=0.00|151=500|14=0|6=0.0000|60=20260929-07:50:25.851|10=085|
20260929-07:50:25.928 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=5
20260929-07:50:25.928 INFO    session  emu42  << ER 11=OE-20260929-075023-2 37=O-20260929-062406-3 150=0(New) 39=0(New) cum=0 leaves=500 avg=0.0000  -> NEW cum=0 leaves=500  checks: PASS
> replace last 800 10.50
20260929-07:50:25.985 OUT  seq=4    35=G  8=FIX.4.2|9=193|35=G|49=AGENT|56=ORDERECHO|34=4|52=20260929-07:50:25.985|41=OE-20260929-075023-2|11=OE-20260929-075023-3|37=O-20260929-062406-3|21=1|55=ZWZZT|54=1|60=20260929-07:50:25.979|38=800|40=2|44=10.50|10=230|
>> G 11=OE-20260929-075023-3 41=OE-20260929-075023-2 qty=800 10.50 sent
20260929-07:50:25.988 IN   seq=6    35=8  8=FIX.4.2|9=262|35=8|49=ORDERECHO|56=AGENT|34=6|52=20260929-07:50:25.987|37=O-20260929-062406-3|11=OE-20260929-075023-3|41=OE-20260929-075023-2|17=E-20260929-062406-7|20=0|150=5|39=0|55=ZWZZT|54=1|38=800|40=2|44=10.50|32=0|31=0.00|151=800|14=0|6=0.0000|60=20260929-07:50:25.987|10=054|
20260929-07:50:25.997 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=6
20260929-07:50:25.997 INFO    session  emu42  << ER 11=OE-20260929-075023-3 37=O-20260929-062406-3 150=5(Replaced) 39=0(New) cum=0 leaves=800 avg=0.0000  -> NEW cum=0 leaves=800  checks: PASS
> cancel last
20260929-07:50:26.167 OUT  seq=5    35=F  8=FIX.4.2|9=174|35=F|49=AGENT|56=ORDERECHO|34=5|52=20260929-07:50:26.167|41=OE-20260929-075023-3|11=OE-20260929-075023-4|37=O-20260929-062406-3|55=ZWZZT|54=1|60=20260929-07:50:26.048|38=800|10=148|
>> F 11=OE-20260929-075023-4 41=OE-20260929-075023-3 sent
20260929-07:50:26.173 IN   seq=7    35=8  8=FIX.4.2|9=260|35=8|49=ORDERECHO|56=AGENT|34=7|52=20260929-07:50:26.173|37=O-20260929-062406-3|11=OE-20260929-075023-4|41=OE-20260929-075023-3|17=E-20260929-062406-8|20=0|150=4|39=4|55=ZWZZT|54=1|38=800|40=2|44=10.50|32=0|31=0.00|151=0|14=0|6=0.0000|60=20260929-07:50:26.170|10=184|
20260929-07:50:26.178 INFO    session  emu42  application message received: 35=8 (ExecutionReport) seq=7
20260929-07:50:26.178 INFO    session  emu42  << ER 11=OE-20260929-075023-4 37=O-20260929-062406-3 150=4(Canceled) 39=4(Canceled) cum=0 leaves=0 avg=0.0000  -> CANCELED cum=0 leaves=0  checks: PASS
> status
CLORDID                      ORDERID                SYMBOL SIDE  TYPE      QTY      CUM   LEAVES        AVG STATE / CHECKS
OE-20260929-075023-1         O-20260929-062406-2    EFG    BUY   LMT      1000      500      500    10.0000 PARTIALLY_FILLED / PASS
OE-20260929-075023-4         O-20260929-062406-3    ZWZZT  BUY   LMT       800        0        0     0.0000 CANCELED / PASS
> timeline last
Order chain for OE-20260929-075023-2
  ClOrdIDs: OE-20260929-075023-2, OE-20260929-075023-3, OE-20260929-075023-4
  OrderID : O-20260929-062406-3

  time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
  2026-09-29T07:50:24.378 <--  NewOrderSingle       - / -                            500              -       -       -          -
  2026-09-29T07:50:25.928 -->  ExecutionReport      0 (New) / 0 (New)                500              -       0     500     0.0000
  2026-09-29T07:50:25.985 <--  OrderCancelReplaceRequest - / -                            800              -       -       -          -
  2026-09-29T07:50:25.997 -->  ExecutionReport      5 (Replaced) / 0 (New)           800              -       0     800     0.0000
  2026-09-29T07:50:26.167 <--  OrderCancelRequest   - / -                            800              -       -       -          -
  2026-09-29T07:50:26.178 -->  ExecutionReport      4 (Canceled) / 4 (Canceled)      800              -       0       0     0.0000

Checks
  [PASS] cum_qty_monotonic: CumQty rose to 0 without ever falling
  [PASS] working_quantities: 2 working report(s) balanced
  [PASS] terminal_quantities: 1 terminal report(s) consistent
  [PASS] fill_quantities_sum: no fills in this chain
  [PASS] avg_px: no fills to average
  [PASS] exec_ids_unique: 3 ExecID(s), all distinct
  [PASS] order_id_constant: OrderID O-20260929-062406-3 throughout
  [PASS] nothing_after_terminal: terminal 39=4 was the last word
  [PASS] version_rules: every report matches its version's conventions
  [PASS] requests_answered: all 3 request(s) answered
  [PASS] framing_intact: all 6 message(s) correctly framed

  verdict: PASS
> quit
20260929-07:50:26.236 OUT  seq=6    35=5  8=FIX.4.2|9=90|35=5|49=AGENT|56=ORDERECHO|34=6|52=20260929-07:50:26.236|58=OrderEcho agent: session done|10=109|
20260929-07:50:26.236 INFO    session  emu42  logout initiated: OrderEcho agent: session done
20260929-07:50:26.236 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
20260929-07:50:26.239 IN   seq=8    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=8|52=20260929-07:50:26.239|58=Logout acknowledged|10=037|
20260929-07:50:26.244 INFO    session  emu42  logout confirmed: Logout acknowledged
20260929-07:50:26.244 INFO    session  emu42  Disconnecting: Logout confirmed
20260929-07:50:26.248 INFO    session  emu42  disconnected: next_out=7 next_in=9
20260929-07:50:26.248 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
20260929-07:50:26.248 INFO    session  emu42  Connection closed, peer=127.0.0.1:9878
20260929-07:50:26.248 INFO    session  engine  Shutdown complete
>>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
exit 0
```

### 3.4 Python vs Go timeline, same file (the agent's `logs/fix/emu42_20260929.log`), ZWZZT chain

Python:
```
$ .venv/bin/python orderecho_LogView.py timeline --no-color --clordid OE-20260929-075023-3 logs/fix/emu42_20260929.log
Order chain for OE-20260929-075023-3
  ClOrdIDs: OE-20260929-075023-2, OE-20260929-075023-3, OE-20260929-075023-4
  OrderID : O-20260929-062406-3

  time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
  2026-09-29T07:50:24.378 <--  New Order            - / -                            500              -       -       -          -
  2026-09-29T07:50:25.923 -->  Execution Report     0 (New) / 0 (New)                500              -       0     500     0.0000
  2026-09-29T07:50:25.985 <--  Order Cancel/Replace Request - / -                            800              -       -       -          -
  2026-09-29T07:50:25.988 -->  Execution Report     5 (Replace) / 0 (New)            800              -       0     800     0.0000
  2026-09-29T07:50:26.167 <--  Order Cancel Request - / -                            800              -       -       -          -
  2026-09-29T07:50:26.173 -->  Execution Report     4 (Canceled) / 4 (Canceled)      800              -       0       0     0.0000

Checks
  [PASS] cum_qty_monotonic: CumQty rose to 0 without ever falling
  [PASS] working_quantities: 2 working report(s) balanced
  [PASS] terminal_quantities: 1 terminal report(s) consistent
  [PASS] fill_quantities_sum: no fills in this chain
  [PASS] avg_px: no fills to average
  [PASS] exec_ids_unique: 3 ExecID(s), all distinct
  [PASS] order_id_constant: OrderID O-20260929-062406-3 throughout
  [PASS] nothing_after_terminal: terminal 39=4 was the last word
  [PASS] version_rules: every report matches its version's conventions
  [PASS] requests_answered: all 3 request(s) answered
  [PASS] framing_intact: all 6 message(s) correctly framed

  verdict: PASS
exit 0
```
Go:
```
$ bin/orderecho timeline logs/fix/emu42_20260929.log --clordid OE-20260929-075023-3
Order chain for OE-20260929-075023-3
  ClOrdIDs: OE-20260929-075023-2, OE-20260929-075023-3, OE-20260929-075023-4
  OrderID : O-20260929-062406-3

  time                  dir  type                 exec/status                      qty           last     cum  leaves        avg
  2026-09-29T07:50:24.378 <--  NewOrderSingle       - / -                            500              -       -       -          -
  2026-09-29T07:50:25.923 -->  ExecutionReport      0 (New) / 0 (New)                500              -       0     500     0.0000
  2026-09-29T07:50:25.985 <--  OrderCancelReplaceRequest - / -                            800              -       -       -          -
  2026-09-29T07:50:25.988 -->  ExecutionReport      5 (Replaced) / 0 (New)           800              -       0     800     0.0000
  2026-09-29T07:50:26.167 <--  OrderCancelRequest   - / -                            800              -       -       -          -
  2026-09-29T07:50:26.173 -->  ExecutionReport      4 (Canceled) / 4 (Canceled)      800              -       0       0     0.0000

Checks
  [PASS] cum_qty_monotonic: CumQty rose to 0 without ever falling
  [PASS] working_quantities: 2 working report(s) balanced
  [PASS] terminal_quantities: 1 terminal report(s) consistent
  [PASS] fill_quantities_sum: no fills in this chain
  [PASS] avg_px: no fills to average
  [PASS] exec_ids_unique: 3 ExecID(s), all distinct
  [PASS] order_id_constant: OrderID O-20260929-062406-3 throughout
  [PASS] nothing_after_terminal: terminal 39=4 was the last word
  [PASS] version_rules: every report matches its version's conventions
  [PASS] requests_answered: all 3 request(s) answered
  [PASS] framing_intact: all 6 message(s) correctly framed

  verdict: PASS
exit 0
```
Per-check statuses and verdicts match. So do the explanations, word for word. The only differences are display names, which come from different dictionaries: `New Order` / `NewOrderSingle`, and `5 (Replace)` / `5 (Replaced)`.

I ran the same status comparison for the EFG chain on the same log and for the AAPL chain on `logs/fix/emu44_20260929.log`. All three are identical.

## 4. Decisions I made (conservative, logged)

1. **Out-of-order queue (3.1): I kept A1's ResendRequest (`7=expected 16=0`).**
   - Messages above next_in are held, the gap-revealing message included, up to 1000 per session. One more triggers Logout "Resend queue overflow".
   - Held messages are released in seq order once the gap closes.
   - The problem: the emulator (like most engines) answers `16=0` by gap-filling admin messages, so its gap fill covers the very TestRequest we are holding. Dropping held messages that a fill "skipped" (QuickFIX's behaviour) would therefore lose the TestRequest, and scenario 10 would be impossible.
   - So a held original whose seq a gap fill covered is **still processed**; we hold the real message and must not lose it (an ER from a `gapfill`-mode counterparty is the same case).
   - A held original whose seq was already processed from its replay is dropped. A held SequenceReset or Logon in a covered position is dropped, not applied out of place.
   - Every queue and dequeue is in evidence, and the queue is emptied, with evidence, on disconnect. See Question 1.
2. **HeartBtInt mismatch (3.2):** `heartbeat_mismatch: warn` (the default) logs a WARNING plus evidence and keeps our own interval. `refuse` keeps A1's Logout.
3. **Refused Logon (3.5):** we reply `Logout 58=Logout acknowledged`, then disconnect. The CLI still exits 1 with their text.
4. **Exit-code precedence for `order`:**
   - A session failure (1, 3 or 4) wins, then 5 (FAIL), then 6 (not terminal in time).
   - `session` exits 5 if any order's verdict is FAIL at the end.
   - Exit 3 is also used when a reconnect after an earlier logon never gets back in.
   - A refusal is always 1.
5. **New config keys** live in `defaults` and can be overridden per session: `heartbeat_mismatch`, `include_handl_inst` (default true), `clordid_prefix` (default OE) and `account` (optional; adds tag 1 on D).
6. **ClOrdIDs are `<prefix>-<run_id>-<n>`.** The counter is per process; run_id has one-second resolution (see Question 4).
7. **F/G linkage:**
   - F and G refer to the chain's most recent ClOrdID that we sent and that is not known to be rejected.
   - The emulator looks orders up by their *current* ClOrdID, so this is what makes a piped `replace` followed at once by `cancel` work.
   - A rejected G reverts to the previous ClOrdID.
   - F carries `38` (the current OrderQty) and `37` when known; G does the same.
   - `replace` without PX keeps the limit price, and a market order takes no price.
8. **Pipe determinism:** the REPL waits up to 5 s for the first answer to each order request before it reads the next command. Commands are echoed with `> ` when stdin isn't a terminal.
9. **Live checks run after every report.** Only status *changes* are logged: WARN/FAIL right away, a recovery to PASS as INFO. Every 35=8/35=9 also writes an `order report` evidence event with the snapshot, including every check's status, in `order`.
10. **State mapping:**
    - OrdStatus 0/1/2/4/8/6/E maps to the named states; A maps to SENT; C and 3 map to CANCELED.
    - An ER with 39=8 on a G/F ClOrdID does not end the order.
    - A session or business reject of the D sets REJECTED.
    - PossDup replays (an ExecID already seen) never change state.
11. **Duplicate ClOrdID:** a 39=8 report carrying a different OrderID for a ClOrdID we sent twice is attached to the duplicate request. The original order is left untouched.
12. **SendRaw requests are tracked** (in history and in `bySeq`), so their answers attach: scenarios 9 and 11.
13. **Deliberate differences from Python** (the three Python findings in §6): Go does not copy those behaviours. Each is pinned by `TestParityKnownDivergences`, and by unit tests in `internal/checks`.
14. **Ported Python test cases:** all of them are ported. In the check-10 session-reject case I gave the D a realistic sender (AGENT), because the Python helper writes every message as ORDERECHO and so relies on finding 1.
15. **A1 tests updated only where A2 changes behaviour:** the refused-logon test now expects our Logout reply, and the 108 mismatch is now refused only under `refuse`, with a new warn test.
16. **Interop emulator config** mirrors `orderecho_multi.yaml` — same sessions, rules and bands, including the strict broker — with static pricing (`AAPL: 227.50, default: 100.00`), temp storage and random ports.
17. **Console echo:** with `console: true`, the report and check lines already appear through the engine-log echo, so the order commands don't print them a second time.
18. **Goldens** were captured once with `go test ./internal/order -update-golden`. Normal runs compare byte for byte.
19. **Decimal parsing** follows Python: whitespace is trimmed, `_` is dropped, exponents are accepted, NaN and Infinity count as "no value". Unicode digits (such as `１２`) are not accepted.
20. **The shadow expectation** (Cum = ΣLastQty, Avg = Σ(q·px)/Σq, Leaves = Qty − Cum while working) uses `math/big` and appears in every snapshot. Interop scenarios 3 and 7 assert exact equality with the reported AvgPx; 10.4000 in scenario 7.

## 5. Questions for me

1. **Queue semantics (Decision 1).** For held messages that a gap fill covers, do you want what I built (process the original we hold)? Or strict QuickFIX behaviour (drop it), together with a closed-interval ResendRequest (`16=<first held seq − 1>`) so a held TestRequest is still answered? The closed interval would change A1's `16=0`, which is why I did not choose it.
2. **Scenario 9 verdict.** A duplicate ClOrdID makes the original order's chain FAIL `order_id_constant`, because the reject carries a second OrderID. Go and Python agree. Is that the verdict you want, or should a reject that answers a *duplicate* request be kept out of the original's chain? That would be a deliberate change on both sides.
3. **Transient WARN.** `requests_answered` can WARN live for a moment, for example after a G is sent but before its ack, and it is logged then. Suppress it while a request is younger than N seconds?
4. **ClOrdID uniqueness across runs.** Two runs started in the same second get the same ClOrdIDs, and the emulator will reject the second as duplicates. Add milliseconds or a random suffix to run_id? That also changes the evidence file name.
5. **Exit codes.** Is the precedence right? Session codes first, then 5, then 6. Should a refusal after an earlier successful logon (on reconnect) be 1, as now, or 3?
6. **Python findings.** Do you want the §6 items fixed in an emulator cook? Once they are, the divergence test flips and the Go notes can go.

## 6. Python-side findings (reported, not fixed)

These are all in `../OrderEchoFixEmulator`, and all three are reproduced by `TestParityKnownDivergences`.

1. **A reject is matched to a request without regard to who sent it** (`orderecho_Timeline._add_session_rejects` and `check_requests_answered`).
   - A 35=3 or 35=j joins a chain, and counts as an answer, if its RefSeqNum equals a request's MsgSeqNum in the same session. The sender is never checked.
   - In one side's log both directions share that key space. So a Reject *we* sent about the counterparty's message N "answers" our own D that happened to be on seq N.
   - Result on a real agent log: Python says PASS for a request nobody answered. Go says WARN.
   - Go ignores a reject whose SenderCompID equals the request's. This is direction-dependent, so per the spec I did not copy it.
   - Related: `tests/test_Timeline.py::line()` writes every message as 49=ORDERECHO, so `test_check_10_counts_a_session_reject_as_an_answer` passes only because of this bug.
2. **Messages are cut short at tags ending in "10="** (`orderecho_LogParse.MESSAGE_PATTERN`).
   - The lazy `.*?10=\d{1,3}<delim>` also matches the tail of 110=, 210=, 1010= and so on.
   - A message containing, say, `110=100|` (MinQty) is cut there and flagged bad CheckSum/BodyLength, so `framing_intact` WARNs, and the fields after the cut are lost.
   - Go ends a message only at a `10=` that starts a field.
3. **Any non-ASCII value breaks the checksum** (`orderecho_LogParse.verify_framing`).
   - Files are read as UTF-8 and re-encoded as latin-1 before the checksum. Any non-ASCII character (e.g. `58=reçu`) therefore gives a different byte count and sum, and the message is flagged bad CheckSum.
   - Go checks the bytes as written.

One observation that is not a check bug:
- The emulator's replay gap-fills a TestRequest (it is an admin type), so with `16=0` its own TestRequest behind a gap is never redelivered. Decision 1 is how the agent still answers it.
