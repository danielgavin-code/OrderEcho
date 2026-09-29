# REPORT_A1 — OrderEcho Go agent, Cook A1

Built end to end: codec, pure session core, FIX 4.2/4.4 profiles, stores, evidence, logs, config and CLI. It is interop-tested against the real Python emulator, and every interop test ran; none were skipped. `make test`, `make interop` and `make lint` (`go vet`, with and without the `interop` tag) all pass.

`../OrderEchoFixEmulator` was not modified. `find ../OrderEchoFixEmulator -newer SPEC_A1.md` returns nothing, `.venv` included. Every emulator run used a working directory outside its folder and `PYTHONDONTWRITEBYTECODE=1`, so no logs, data or `.pyc` files landed there.

## 1. Files created

```
Makefile                                 build / test / interop / lint (go vet)
go.mod, go.sum                           (go.mod existed; added gopkg.in/yaml.v3 v3.0.1)
config/orderecho.yaml                    shipped config (spec §8, verbatim values)
cmd/orderecho/main.go                    CLI: version, sessions, connect, status
internal/version/version.go              Version = "0.1.0", Build = "a1"
internal/clock/clock.go                  SystemClock, FakeClock
internal/fix/codec/codec.go              framing, 9/10 validation, DiscardedFrame, Encode/Build/Rebuild, ToPipe
internal/fix/codec/codec_test.go
internal/fix/profile/profile.go          FIX42, FIX44 (BeginString, admin/app types, MsgType + 373 names)
internal/fix/session/session.go          PURE initiator session core
internal/fix/session/session_test.go
internal/fix/transport/transport.go      TCP initiator: connect, read loop, timer tick, reconnect, commands
internal/store/store.go                  seqnum store (atomic), outbound message store (JSONL, archive)
internal/store/store_test.go
internal/evidence/evidence.go            JSONL evidence writer (emulator schema)
internal/evidence/evidence_test.go
internal/logs/logs.go                    FIX log + engine log (emulator line formats, daily UTC rollover)
internal/logs/logs_test.go
internal/config/config.go                YAML load + validation
internal/config/config_test.go
internal/interop/harness_test.go         (build tag interop) emulator/agent/CLI harness
internal/interop/interop_test.go         (build tag interop) scenarios 1-8 + Ctrl+C
testdata/emulator_lines.txt              20 real emulator FIX lines (see Decisions 17)
REPORT_A1.md
```

The runtime output of the §10.3 real run is in `data/` and `logs/`, both gitignored: `data/seqnums`, `data/msgstore`, `data/evidence`, `logs/fix/{emu42,emu44,strict}_20260929.log` and `logs/engine/orderecho_20260929.log`. `bin/orderecho` comes from `make build`.

## 2. `make test` and `make interop`

### `make test`
```
go test -count=1 ./...
?   	github.com/danielgavin-code/OrderEcho/cmd/orderecho	[no test files]
?   	github.com/danielgavin-code/OrderEcho/internal/clock	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/config	0.746s
ok  	github.com/danielgavin-code/OrderEcho/internal/evidence	0.457s
ok  	github.com/danielgavin-code/OrderEcho/internal/fix/codec	1.871s
?   	github.com/danielgavin-code/OrderEcho/internal/fix/profile	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/fix/session	0.901s
?   	github.com/danielgavin-code/OrderEcho/internal/fix/transport	[no test files]
ok  	github.com/danielgavin-code/OrderEcho/internal/logs	1.192s
ok  	github.com/danielgavin-code/OrderEcho/internal/store	1.907s
?   	github.com/danielgavin-code/OrderEcho/internal/version	[no test files]
```

### `make lint`
```
go vet ./...
go vet -tags interop ./...
```

### `make interop` (full output)

Summary: 9/9 PASS, 0 SKIP. Each test started its own emulator, `.venv/bin/python orderecho_Main.py --config <tmp>`, with all storage in a temp dir and random free FIX, strict and API ports. It waited for `/health` and sent SIGTERM (then kill) at the end. The CLI tests ran a freshly built agent binary.

```
go build -o bin/orderecho ./cmd/orderecho
go test -count=1 -tags interop -v ./internal/interop/
=== RUN   TestScenario1LogonTestRequestLogout
    interop_test.go:26: emulator up: fix=63160 strict=63161 api=63162 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario1LogonTestRequestLogout2457713808/001/emulator
    interop_test.go:34: emu42: TestRequest TEST-1 answered
    interop_test.go:35: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:51: emu44: TestRequest TEST-1 answered
    interop_test.go:52: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:53: POST /sessions/agent44/logout -> {"state":"LOGOUT_SENT","sent":[{"seq":3,"msg_type":"5","raw":"8=FIX.4.4|9=85|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-06:13:44.197|58=interop: emulator logout|10=036|","injected":false}]}
    interop_test.go:75: $ orderecho connect --session emu42 --test-request --duration 1s  -> exit 0
        OrderEcho agent 0.1.0 (a1) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:63160
          heartbeat      : 30s  reset_on_logon=true  reconnect=false
          seqnums        : data/seqnums/emu42.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-061344.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-06:13:44.655 INFO    session  engine  Startup: version=0.1.0 build=a1 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:63160 evidence=data/evidence/20260929-061344.jsonl
        20260929-06:13:44.655 INFO    session  emu42  Connecting to 127.0.0.1:63160
        20260929-06:13:44.656 INFO    session  emu42  Connected to 127.0.0.1:63160 (local 127.0.0.1:63311)
        20260929-06:13:44.855 INFO    session  emu42  connected: sending Logon
        20260929-06:13:44.855 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-06:13:44.855 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-06:13:44.854|98=0|108=30|141=Y|10=072|
        20260929-06:13:44.855 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-06:13:44.860 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-06:13:44.859|98=0|108=30|141=Y|10=077|
        20260929-06:13:44.924 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-06:13:44.924 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-06:13:45.037 OUT  seq=2    35=1  8=FIX.4.2|9=68|35=1|49=AGENT|56=ORDERECHO|34=2|52=20260929-06:13:45.036|112=TEST-1|10=094|
        >>> TestRequest TEST-1 sent
        20260929-06:13:45.039 IN   seq=2    35=0  8=FIX.4.2|9=68|35=0|49=ORDERECHO|56=AGENT|34=2|52=20260929-06:13:45.039|112=TEST-1|10=096|
        20260929-06:13:45.044 INFO    session  emu42  testrequest answered: 112=TEST-1
        >>> TestRequest TEST-1 answered in 7ms
        >>> Duration 1s elapsed; logging out
        20260929-06:13:46.124 OUT  seq=3    35=5  8=FIX.4.2|9=94|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-06:13:46.124|58=OrderEcho agent: duration elapsed|10=004|
        20260929-06:13:46.124 INFO    session  emu42  logout initiated: OrderEcho agent: duration elapsed
        20260929-06:13:46.124 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
        20260929-06:13:46.127 IN   seq=3    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-06:13:46.126|58=Logout acknowledged|10=027|
        20260929-06:13:46.131 INFO    session  emu42  logout confirmed: Logout acknowledged
        20260929-06:13:46.131 INFO    session  emu42  Disconnecting: Logout confirmed
        20260929-06:13:46.136 INFO    session  emu42  disconnected: next_out=4 next_in=4
        20260929-06:13:46.136 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
        20260929-06:13:46.136 INFO    session  emu42  Connection closed, peer=127.0.0.1:63160
        20260929-06:13:46.136 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    interop_test.go:75: $ orderecho connect --session emu44 --test-request --duration 1s  -> exit 0
        OrderEcho agent 0.1.0 (a1) - session emu44
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:63160
          heartbeat      : 30s  reset_on_logon=true  reconnect=false
          seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-061346.jsonl
          fix log        : logs/fix/emu44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-06:13:46.144 INFO    session  engine  Startup: version=0.1.0 build=a1 config=orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:63160 evidence=data/evidence/20260929-061346.jsonl
        20260929-06:13:46.144 INFO    session  emu44  Connecting to 127.0.0.1:63160
        20260929-06:13:46.144 INFO    session  emu44  Connected to 127.0.0.1:63160 (local 127.0.0.1:63312)
        20260929-06:13:46.154 INFO    session  emu44  connected: sending Logon
        20260929-06:13:46.154 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
        20260929-06:13:46.154 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-06:13:46.154|98=0|108=30|141=Y|10=069|
        20260929-06:13:46.155 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
        20260929-06:13:46.158 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-06:13:46.158|98=0|108=30|141=Y|10=073|
        20260929-06:13:46.162 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-06:13:46.162 INFO    session  emu44  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-06:13:46.168 OUT  seq=2    35=1  8=FIX.4.4|9=68|35=1|49=AGENT|56=ORDERECHO|34=2|52=20260929-06:13:46.168|112=TEST-1|10=103|
        >>> TestRequest TEST-1 sent
        20260929-06:13:46.170 IN   seq=2    35=0  8=FIX.4.4|9=68|35=0|49=ORDERECHO|56=AGENT|34=2|52=20260929-06:13:46.170|112=TEST-1|10=095|
        20260929-06:13:46.175 INFO    session  emu44  testrequest answered: 112=TEST-1
        >>> TestRequest TEST-1 answered in 7ms
        >>> Duration 1s elapsed; logging out
        20260929-06:13:47.243 OUT  seq=3    35=5  8=FIX.4.4|9=94|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-06:13:47.243|58=OrderEcho agent: duration elapsed|10=009|
        20260929-06:13:47.243 INFO    session  emu44  logout initiated: OrderEcho agent: duration elapsed
        20260929-06:13:47.243 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
        20260929-06:13:47.246 IN   seq=3    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-06:13:47.245|58=Logout acknowledged|10=032|
        20260929-06:13:47.252 INFO    session  emu44  logout confirmed: Logout acknowledged
        20260929-06:13:47.252 INFO    session  emu44  Disconnecting: Logout confirmed
        20260929-06:13:47.256 INFO    session  emu44  disconnected: next_out=4 next_in=4
        20260929-06:13:47.256 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
        20260929-06:13:47.256 INFO    session  emu44  Connection closed, peer=127.0.0.1:63160
        20260929-06:13:47.257 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
    interop_test.go:89: POST /sessions/agent42/logout -> {"state":"LOGOUT_SENT","sent":[{"seq":2,"msg_type":"5","raw":"8=FIX.4.2|9=78|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-06:13:47.423|58=bye from emulator|10=061|","injected":false}]}
    interop_test.go:87: $ orderecho connect --session emu42  -> exit 0
        OrderEcho agent 0.1.0 (a1) - session emu42
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:63160
          heartbeat      : 30s  reset_on_logon=true  reconnect=false
          seqnums        : data/seqnums/emu42.json (next_out=4 next_in=4)
          evidence file  : data/evidence/20260929-061347.jsonl
          fix log        : logs/fix/emu42_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-06:13:47.264 INFO    session  engine  Startup: version=0.1.0 build=a1 config=orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:63160 evidence=data/evidence/20260929-061347.jsonl
        20260929-06:13:47.304 INFO    session  emu42  Connecting to 127.0.0.1:63160
        20260929-06:13:47.305 INFO    session  emu42  Connected to 127.0.0.1:63160 (local 127.0.0.1:63314)
        20260929-06:13:47.374 INFO    session  emu42  connected: sending Logon
        20260929-06:13:47.374 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
        20260929-06:13:47.374 INFO    session  emu42  store archived: outbound message store archived to data/msgstore/emu42.jsonl.20260929-061347 (Logon will carry 141=Y)
        20260929-06:13:47.374 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-06:13:47.374|98=0|108=30|141=Y|10=072|
        20260929-06:13:47.374 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
        20260929-06:13:47.378 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-06:13:47.378|98=0|108=30|141=Y|10=076|
        20260929-06:13:47.383 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-06:13:47.383 INFO    session  emu42  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        20260929-06:13:47.423 IN   seq=2    35=5  8=FIX.4.2|9=78|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-06:13:47.423|58=bye from emulator|10=061|
        20260929-06:13:47.432 INFO    session  emu42  logout received: bye from emulator
        20260929-06:13:47.432 OUT  seq=2    35=5  8=FIX.4.2|9=80|35=5|49=AGENT|56=ORDERECHO|34=2|52=20260929-06:13:47.432|58=Logout acknowledged|10=027|
        20260929-06:13:47.432 INFO    session  emu42  Disconnecting: Logout requested by counterparty
        20260929-06:13:47.432 INFO    session  emu42  State ACTIVE -> DISCONNECTED
        20260929-06:13:47.441 INFO    session  emu42  disconnected: next_out=3 next_in=3
        20260929-06:13:47.441 INFO    session  emu42  Connection closed, peer=127.0.0.1:63160
        20260929-06:13:47.441 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by counterparty) (their 58: "bye from emulator")
--- PASS: TestScenario1LogonTestRequestLogout (18.92s)
=== RUN   TestScenario2EmulatorTestRequest
    interop_test.go:98: emulator up: fix=63319 strict=63320 api=63321 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario2EmulatorTestRequest580955542/001/emulator
    interop_test.go:101: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":2,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=2|52=20260929-06:13:59.938|112=TEST-1|10=110|","injected":false}]}
    interop_test.go:114: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
--- PASS: TestScenario2EmulatorTestRequest (12.38s)
=== RUN   TestScenario3EmulatorSeqGap
    interop_test.go:120: emulator up: fix=63439 strict=63440 api=63441 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario3EmulatorSeqGap2910982328/001/emulator
    interop_test.go:124: POST /sessions/agent42/inject/seq-gap -> {"skipped":3,"next_out":5}
    interop_test.go:126: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":5,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=5|52=20260929-06:14:05.927|112=TEST-1|10=103|","injected":false}]}
    interop_test.go:143: in sync: agent next_out=3 next_in=6 / emulator next_in=3 next_out=6 (ACTIVE)
    interop_test.go:144: emu42: TestRequest TEST-1 answered
    interop_test.go:145: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-2","sent":[{"seq":7,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=7|52=20260929-06:14:06.068|112=TEST-2|10=103|","injected":false}]}
    interop_test.go:150: in sync: agent next_out=5 next_in=8 / emulator next_in=5 next_out=8 (ACTIVE)
--- PASS: TestScenario3EmulatorSeqGap (6.16s)
=== RUN   TestScenario4AgentSkipOutboundSeq
    interop_test.go:156: emulator up: fix=63501 strict=63502 api=63503 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario4AgentSkipOutboundSeq4161054674/001/emulator
    interop_test.go:188: in sync: agent next_out=6 next_in=3 / emulator next_in=6 next_out=3 (ACTIVE)
    interop_test.go:189: emu44: TestRequest TEST-2 answered
    interop_test.go:190: in sync: agent next_out=7 next_in=4 / emulator next_in=7 next_out=4 (ACTIVE)
    interop_test.go:201: in sync: agent next_out=8 next_in=5 / emulator next_in=8 next_out=5 (ACTIVE)
--- PASS: TestScenario4AgentSkipOutboundSeq (2.71s)
=== RUN   TestScenario5ReconnectWithoutReset
    interop_test.go:207: emulator up: fix=63530 strict=63531 api=63532 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario5ReconnectWithoutReset563948141/001/emulator
    interop_test.go:210: emu42: TestRequest TEST-1 answered
    interop_test.go:211: in sync: agent next_out=3 next_in=3 / emulator next_in=3 next_out=3 (ACTIVE)
    interop_test.go:213: POST /sessions/agent42/disconnect -> {"disconnected":true}
    interop_test.go:239: in sync: agent next_out=4 next_in=4 / emulator next_in=4 next_out=4 (ACTIVE)
    interop_test.go:240: emu42: TestRequest TEST-2 answered
    interop_test.go:241: in sync: agent next_out=5 next_in=5 / emulator next_in=5 next_out=5 (ACTIVE)
--- PASS: TestScenario5ReconnectWithoutReset (2.08s)
=== RUN   TestScenario6WrongVersion
    interop_test.go:253: emulator up: fix=63547 strict=63548 api=63549 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario6WrongVersion3673925402/001/emulator
    interop_test.go:255: $ orderecho connect --session strict44  -> exit 1
        OrderEcho agent 0.1.0 (a1) - session strict44
          config         : orderecho.yaml
          route          : AGENT -> STRICTBRK  FIX.4.4  127.0.0.1:63548
          heartbeat      : 30s  reset_on_logon=true  reconnect=false
          seqnums        : data/seqnums/strict44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-061411.jsonl
          fix log        : logs/fix/strict44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-06:14:11.743 INFO    session  engine  Startup: version=0.1.0 build=a1 config=orderecho.yaml session=strict44 FIX.4.4 AGENT->STRICTBRK@127.0.0.1:63548 evidence=data/evidence/20260929-061411.jsonl
        20260929-06:14:11.743 INFO    session  strict44  Connecting to 127.0.0.1:63548
        20260929-06:14:11.744 INFO    session  strict44  Connected to 127.0.0.1:63548 (local 127.0.0.1:63557)
        20260929-06:14:11.783 INFO    session  strict44  connected: sending Logon
        20260929-06:14:11.783 INFO    session  strict44  seqnums reset: Logon will carry 141=Y
        20260929-06:14:11.783 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-06:14:11.782|98=0|108=30|141=Y|10=098|
        20260929-06:14:11.783 INFO    session  strict44  State DISCONNECTED -> LOGON_SENT
        20260929-06:14:11.786 IN   seq=1    35=5  8=FIX.4.2|9=100|35=5|49=STRICTBRK|56=AGENT|34=1|52=20260929-06:14:11.785|58=Incorrect BeginString, expected FIX.4.2|10=109|
        20260929-06:14:11.786 WARNING session  strict44  logon refused: counterparty answered Logon with Logout: Incorrect BeginString, expected FIX.4.2
        20260929-06:14:11.786 INFO    session  strict44  Disconnecting: Logon refused by counterparty: Incorrect BeginString, expected FIX.4.2
        20260929-06:14:11.790 INFO    session  strict44  disconnected: next_out=2 next_in=1
        20260929-06:14:11.790 INFO    session  strict44  State LOGON_SENT -> DISCONNECTED
        20260929-06:14:11.790 INFO    session  strict44  Connection closed, peer=127.0.0.1:63548
        20260929-06:14:11.790 INFO    session  engine  Shutdown complete
        >>> LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
        orderecho: LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
    interop_test.go:274: $ orderecho connect --session strict --duration 500ms  -> exit 0
        OrderEcho agent 0.1.0 (a1) - session strict
          config         : orderecho.yaml
          route          : AGENT -> STRICTBRK  FIX.4.2  127.0.0.1:63548
          heartbeat      : 30s  reset_on_logon=true  reconnect=false
          seqnums        : data/seqnums/strict.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-061411.jsonl
          fix log        : logs/fix/strict_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-06:14:11.837 INFO    session  engine  Startup: version=0.1.0 build=a1 config=orderecho.yaml session=strict FIX.4.2 AGENT->STRICTBRK@127.0.0.1:63548 evidence=data/evidence/20260929-061411.jsonl
        20260929-06:14:11.837 INFO    session  strict  Connecting to 127.0.0.1:63548
        20260929-06:14:11.837 INFO    session  strict  Connected to 127.0.0.1:63548 (local 127.0.0.1:63558)
        20260929-06:14:11.846 INFO    session  strict  connected: sending Logon
        20260929-06:14:11.846 INFO    session  strict  seqnums reset: Logon will carry 141=Y
        20260929-06:14:11.846 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-06:14:11.845|98=0|108=30|141=Y|10=096|
        20260929-06:14:11.846 INFO    session  strict  State DISCONNECTED -> LOGON_SENT
        20260929-06:14:11.850 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=STRICTBRK|56=AGENT|34=1|52=20260929-06:14:11.850|98=0|108=30|141=Y|10=092|
        20260929-06:14:11.859 INFO    session  strict  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-06:14:11.859 INFO    session  strict  State LOGON_SENT -> ACTIVE
        >>> Logged on to STRICTBRK as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        >>> Duration 500ms elapsed; logging out
        20260929-06:14:12.432 OUT  seq=2    35=5  8=FIX.4.2|9=94|35=5|49=AGENT|56=STRICTBRK|34=2|52=20260929-06:14:12.432|58=OrderEcho agent: duration elapsed|10=028|
        20260929-06:14:12.433 INFO    session  strict  logout initiated: OrderEcho agent: duration elapsed
        20260929-06:14:12.433 INFO    session  strict  State ACTIVE -> LOGOUT_SENT
        20260929-06:14:12.435 IN   seq=2    35=5  8=FIX.4.2|9=80|35=5|49=STRICTBRK|56=AGENT|34=2|52=20260929-06:14:12.434|58=Logout acknowledged|10=051|
        20260929-06:14:12.439 INFO    session  strict  logout confirmed: Logout acknowledged
        20260929-06:14:12.439 INFO    session  strict  Disconnecting: Logout confirmed
        20260929-06:14:12.445 INFO    session  strict  disconnected: next_out=3 next_in=3
        20260929-06:14:12.445 INFO    session  strict  State LOGOUT_SENT -> DISCONNECTED
        20260929-06:14:12.445 INFO    session  strict  Connection closed, peer=127.0.0.1:63548
        20260929-06:14:12.445 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
--- PASS: TestScenario6WrongVersion (1.57s)
=== RUN   TestScenario7UnknownCompIDs
    interop_test.go:282: emulator up: fix=63559 strict=63560 api=63561 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario7UnknownCompIDs3282149947/001/emulator
    interop_test.go:284: $ orderecho connect --session unknown  -> exit 1
        OrderEcho agent 0.1.0 (a1) - session unknown
          config         : orderecho.yaml
          route          : NOBODY -> ORDERECHO  FIX.4.2  127.0.0.1:63559
          heartbeat      : 30s  reset_on_logon=true  reconnect=false
          seqnums        : data/seqnums/unknown.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-061413.jsonl
          fix log        : logs/fix/unknown_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-06:14:13.720 INFO    session  engine  Startup: version=0.1.0 build=a1 config=orderecho.yaml session=unknown FIX.4.2 NOBODY->ORDERECHO@127.0.0.1:63559 evidence=data/evidence/20260929-061413.jsonl
        20260929-06:14:13.720 INFO    session  unknown  Connecting to 127.0.0.1:63559
        20260929-06:14:13.720 INFO    session  unknown  Connected to 127.0.0.1:63559 (local 127.0.0.1:63573)
        20260929-06:14:13.830 INFO    session  unknown  connected: sending Logon
        20260929-06:14:13.830 INFO    session  unknown  seqnums reset: Logon will carry 141=Y
        20260929-06:14:13.830 OUT  seq=1    35=A  8=FIX.4.2|9=76|35=A|49=NOBODY|56=ORDERECHO|34=1|52=20260929-06:14:13.829|98=0|108=30|141=Y|10=164|
        20260929-06:14:13.830 INFO    session  unknown  State DISCONNECTED -> LOGON_SENT
        20260929-06:14:13.831 WARNING session  unknown  connection closed by counterparty
        20260929-06:14:13.835 INFO    session  unknown  disconnected: next_out=2 next_in=1
        20260929-06:14:13.835 INFO    session  unknown  State LOGON_SENT -> DISCONNECTED
        20260929-06:14:13.835 INFO    session  unknown  Connection closed, peer=127.0.0.1:63559
        20260929-06:14:13.835 INFO    session  engine  Shutdown complete
        >>> counterparty at 127.0.0.1:63559 closed the connection without answering our Logon (no Logout, no reason given) - check sender_comp_id=NOBODY, target_comp_id=ORDERECHO, fix_version=FIX.4.2 and the port
        orderecho: counterparty at 127.0.0.1:63559 closed the connection without answering our Logon (no Logout, no reason given) - check sender_comp_id=NOBODY, target_comp_id=ORDERECHO, fix_version=FIX.4.2 and the port
--- PASS: TestScenario7UnknownCompIDs (1.36s)
=== RUN   TestScenario8ViewerReadsAgentLogs
    interop_test.go:305: emulator up: fix=63574 strict=63575 api=63576 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestScenario8ViewerReadsAgentLogs3462233402/001/emulator
    interop_test.go:308: emu42: TestRequest TEST-1 answered
    interop_test.go:309: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-1","sent":[{"seq":3,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=3|52=20260929-06:14:14.957|112=TEST-1|10=104|","injected":false}]}
    interop_test.go:312: POST /sessions/agent42/inject/seq-gap -> {"skipped":2,"next_out":6}
    interop_test.go:313: POST /sessions/agent42/test-request -> {"test_req_id":"TEST-2","sent":[{"seq":6,"msg_type":"1","raw":"8=FIX.4.2|9=68|35=1|49=ORDERECHO|56=AGENT|34=6|52=20260929-06:14:14.976|112=TEST-2|10=109|","injected":false}]}
    interop_test.go:319: in sync: agent next_out=8 next_in=8 / emulator next_in=8 next_out=8 (ACTIVE)
    interop_test.go:320: emu42: TestRequest TEST-3 answered
    interop_test.go:321: in sync: agent next_out=9 next_in=9 / emulator next_in=9 next_out=9 (ACTIVE)
    interop_test.go:351: viewer stats:
        16 message(s) from 1 file(s)
          first: 2026-09-29T06:14:14.847000+00:00
          last : 2026-09-29T06:14:15.273000+00:00
        
        By session
          emu42  16
        
        By message type
          1 Test Request    5
          0 Heartbeat       3
          2 Resend Request  2
          4 Sequence Reset  2
          5 Logout          2
          A Logon           2
        
        By direction
          in   8
          out  8
        
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
        20260929-06:14:14.847 <-- emu42              1 Logon                  141=Y 108=30
        20260929-06:14:14.851 --> emu42              1 Logon                  141=Y 108=30
        20260929-06:14:14.903 <-- emu42              2 Test Request           112=TEST-1
        20260929-06:14:14.906 --> emu42              2 Heartbeat              112=TEST-1
        20260929-06:14:14.958 --> emu42              3 Test Request           112=TEST-1
        20260929-06:14:14.969 <-- emu42              3 Heartbeat              112=TEST-1
        20260929-06:14:14.977 --> emu42              6 Test Request           112=TEST-2
        20260929-06:14:14.981 <-- emu42              4 Resend Request         7=4 16=0
        20260929-06:14:14.983 --> emu42              4 Sequence Reset         36=7 123=Y [POSSDUP]
        20260929-06:14:15.145 <-- emu42              7 Test Request           112=TEST-2
        20260929-06:14:15.147 --> emu42              7 Resend Request         7=5 16=0
        20260929-06:14:15.152 <-- emu42              5 Sequence Reset         36=8 123=Y [POSSDUP]
        20260929-06:14:15.213 <-- emu42              8 Test Request           112=TEST-3
        20260929-06:14:15.215 --> emu42              8 Heartbeat              112=TEST-3
        20260929-06:14:15.271 <-- emu42              9 Logout                 58=viewer test done
        20260929-06:14:15.273 --> emu42              9 Logout                 58=Logout acknowledged
    interop_test.go:400: viewer stats on evidence:
        16 message(s) from 1 file(s)
          first: 2026-09-29T06:14:14.847000+00:00
          last : 2026-09-29T06:14:15.273000+00:00
        
        By session
          emu42  16
        
        By message type
          1 Test Request    5
          0 Heartbeat       3
          2 Resend Request  2
          4 Sequence Reset  2
          5 Logout          2
          A Logon           2
        
        By direction
          in   8
          out  8
        
        Rejects by reason
          (none)
        
        Other
          resend requests   2
          gap fills         2
          injected          0
          bad checksum      0
          bad body length   0
          unparseable lines 0
--- PASS: TestScenario8ViewerReadsAgentLogs (2.08s)
=== RUN   TestCtrlCLogsOutCleanly
    interop_test.go:420: emulator up: fix=63595 strict=63596 api=63597 dir=/var/folders/_h/5vrdr1s52tqfwj0zgz8jwz7h0000gn/T/TestCtrlCLogsOutCleanly1202618170/001/emulator
    interop_test.go:432: CLI output:
        OrderEcho agent 0.1.0 (a1) - session emu44
          config         : orderecho.yaml
          route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:63595
          heartbeat      : 30s  reset_on_logon=true  reconnect=false
          seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
          evidence file  : data/evidence/20260929-061416.jsonl
          fix log        : logs/fix/emu44_20260929.log
          engine log     : logs/engine/orderecho_20260929.log
          Ctrl+C to log out; Ctrl+C again to exit at once.
        20260929-06:14:16.955 INFO    session  engine  Startup: version=0.1.0 build=a1 config=orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:63595 evidence=data/evidence/20260929-061416.jsonl
        20260929-06:14:16.956 INFO    session  emu44  Connecting to 127.0.0.1:63595
        20260929-06:14:16.956 INFO    session  emu44  Connected to 127.0.0.1:63595 (local 127.0.0.1:63608)
        20260929-06:14:17.085 INFO    session  emu44  connected: sending Logon
        20260929-06:14:17.085 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
        20260929-06:14:17.085 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-06:14:17.084|98=0|108=30|141=Y|10=070|
        20260929-06:14:17.085 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
        20260929-06:14:17.088 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-06:14:17.088|98=0|108=30|141=Y|10=074|
        20260929-06:14:17.093 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
        20260929-06:14:17.093 INFO    session  emu44  State LOGON_SENT -> ACTIVE
        >>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
        
        >>> Ctrl+C: logging out (Ctrl+C again to exit at once)
        20260929-06:14:17.114 OUT  seq=2    35=5  8=FIX.4.4|9=90|35=5|49=AGENT|56=ORDERECHO|34=2|52=20260929-06:14:17.114|58=OrderEcho agent shutting down|10=175|
        20260929-06:14:17.114 INFO    session  emu44  logout initiated: OrderEcho agent shutting down
        20260929-06:14:17.114 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
        20260929-06:14:17.117 IN   seq=2    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=2|52=20260929-06:14:17.116|58=Logout acknowledged|10=026|
        20260929-06:14:17.121 INFO    session  emu44  logout confirmed: Logout acknowledged
        20260929-06:14:17.121 INFO    session  emu44  Disconnecting: Logout confirmed
        20260929-06:14:17.125 INFO    session  emu44  disconnected: next_out=3 next_in=3
        20260929-06:14:17.125 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
        20260929-06:14:17.125 INFO    session  emu44  Connection closed, peer=127.0.0.1:63595
        20260929-06:14:17.125 INFO    session  engine  Shutdown complete
        >>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
--- PASS: TestCtrlCLogsOutCleanly (1.21s)
PASS
ok  	github.com/danielgavin-code/OrderEcho/internal/interop	54.170s
```

How the spec's scenarios map to tests:

| §9 scenario | Test | What it checks |
|---|---|---|
| 1 | `TestScenario1LogonTestRequestLogout` | 4.2 and 4.4 logon, TestRequest round trip, seqnums in sync with the emulator's `/status`. Agent-initiated logout on 4.2, emulator-initiated on 4.4. Both again through the CLI (exit 0). Every line of the 4.4 log is `8=FIX.4.4`. |
| 2 | `TestScenario2EmulatorTestRequest` | `POST /sessions/agent42/test-request` is answered; the emulator's pending id clears. |
| 3 | `TestScenario3EmulatorSeqGap` | `inject/seq-gap {"skip":3}` leads to exactly one ResendRequest `7=<expected> 16=0`, a gap fill received, both sides in sync, and TestRequest round trips afterwards in both directions. |
| 4 | `TestScenario4AgentSkipOutboundSeq` | `SkipOutboundSeq(3)` leads to the emulator's ResendRequest, then one gap fill (`43=Y 122 123=Y 36=next_out`), injected evidence, sync, and a round trip. Also a `SendRaw` app message. |
| 5 | `TestScenario5ReconnectWithoutReset` | Emulator `/disconnect`, then reconnect. The first Logon carries `141=Y`; the second has no 141 and sits on the old next_out. Seqnums continue, sync holds, round trip, clean logout. |
| 6 | `TestScenario6WrongVersion` | CLI with 4.4 against the strict broker: exit 1, "LOGON REFUSED … Incorrect BeginString, expected FIX.4.2", recorded in evidence. The same session on 4.2 exits 0. |
| 7 | `TestScenario7UnknownCompIDs` | CLI exit 1 with an explicit "closed the connection without answering our Logon … check sender_comp_id=…" message. The emulator logged "unknown session". |
| 8 | `TestScenario8ViewerReadsAgentLogs` | `orderecho_LogView.py stats` and `view --no-color` run on the agent's FIX log, which includes resend and gap-fill traffic. Every message is parsed, 0 unparseable, 0 bad checksum or length, and in/out counts and arrows match the log. The evidence JSONL parses in Go and in the viewer (same message count). |
| Ctrl+C | `TestCtrlCLogsOutCleanly` | SIGINT sends a Logout with `58=OrderEcho agent shutting down`; exit 0. |

## 3. Real run (§10.3)

I started the emulator with its own `config/orderecho_multi.yaml`, live pricing, on ports 9878, 9879 and 8090. Its working directory was in my scratchpad. The agent ran from this repo with the shipped `config/orderecho.yaml`. For the strict refusal I used a copy of that config in which only `strict`'s `fix_version` is FIX.4.4, because the CLI has no version-override flag (see Question 4).

### `bin/orderecho connect --session emu42 --test-request --duration 10s`
```
OrderEcho agent 0.1.0 (a1) - session emu42
  config         : config/orderecho.yaml
  route          : AGENT -> ORDERECHO  FIX.4.2  127.0.0.1:9878
  heartbeat      : 30s  reset_on_logon=true  reconnect=false
  seqnums        : data/seqnums/emu42.json (next_out=1 next_in=1)
  evidence file  : data/evidence/20260929-061445.jsonl
  fix log        : logs/fix/emu42_20260929.log
  engine log     : logs/engine/orderecho_20260929.log
  Ctrl+C to log out; Ctrl+C again to exit at once.
20260929-06:14:45.306 INFO    session  engine  Startup: version=0.1.0 build=a1 config=config/orderecho.yaml session=emu42 FIX.4.2 AGENT->ORDERECHO@127.0.0.1:9878 evidence=data/evidence/20260929-061445.jsonl
20260929-06:14:45.306 INFO    session  emu42  Connecting to 127.0.0.1:9878
20260929-06:14:45.307 INFO    session  emu42  Connected to 127.0.0.1:9878 (local 127.0.0.1:63621)
20260929-06:14:45.553 INFO    session  emu42  connected: sending Logon
20260929-06:14:45.553 INFO    session  emu42  seqnums reset: Logon will carry 141=Y
20260929-06:14:45.553 OUT  seq=1    35=A  8=FIX.4.2|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-06:14:45.552|98=0|108=30|141=Y|10=069|
20260929-06:14:45.554 INFO    session  emu42  State DISCONNECTED -> LOGON_SENT
20260929-06:14:45.562 IN   seq=1    35=A  8=FIX.4.2|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-06:14:45.561|98=0|108=30|141=Y|10=069|
20260929-06:14:45.572 INFO    session  emu42  logon accepted: HeartBtInt=30, next_in=2 next_out=2
20260929-06:14:45.572 INFO    session  emu42  State LOGON_SENT -> ACTIVE
>>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
20260929-06:14:45.581 OUT  seq=2    35=1  8=FIX.4.2|9=68|35=1|49=AGENT|56=ORDERECHO|34=2|52=20260929-06:14:45.581|112=TEST-1|10=100|
>>> TestRequest TEST-1 sent
20260929-06:14:45.583 IN   seq=2    35=0  8=FIX.4.2|9=68|35=0|49=ORDERECHO|56=AGENT|34=2|52=20260929-06:14:45.583|112=TEST-1|10=101|
20260929-06:14:45.587 INFO    session  emu42  testrequest answered: 112=TEST-1
>>> TestRequest TEST-1 answered in 6ms
>>> Duration 10s elapsed; logging out
20260929-06:14:55.892 OUT  seq=3    35=5  8=FIX.4.2|9=94|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-06:14:55.891|58=OrderEcho agent: duration elapsed|10=016|
20260929-06:14:55.892 INFO    session  emu42  logout initiated: OrderEcho agent: duration elapsed
20260929-06:14:55.892 INFO    session  emu42  State ACTIVE -> LOGOUT_SENT
20260929-06:14:56.045 IN   seq=3    35=5  8=FIX.4.2|9=80|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-06:14:56.044|58=Logout acknowledged|10=028|
20260929-06:14:56.125 INFO    session  emu42  logout confirmed: Logout acknowledged
20260929-06:14:56.125 INFO    session  emu42  Disconnecting: Logout confirmed
20260929-06:14:56.134 INFO    session  emu42  disconnected: next_out=4 next_in=4
20260929-06:14:56.134 INFO    session  emu42  State LOGOUT_SENT -> DISCONNECTED
20260929-06:14:56.134 INFO    session  emu42  Connection closed, peer=127.0.0.1:9878
20260929-06:14:56.134 INFO    session  engine  Shutdown complete
>>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
exit 0
```

### `bin/orderecho connect --session emu44 --test-request --duration 10s`
```
OrderEcho agent 0.1.0 (a1) - session emu44
  config         : config/orderecho.yaml
  route          : AGENT -> ORDERECHO  FIX.4.4  127.0.0.1:9878
  heartbeat      : 30s  reset_on_logon=true  reconnect=false
  seqnums        : data/seqnums/emu44.json (next_out=1 next_in=1)
  evidence file  : data/evidence/20260929-061456.jsonl
  fix log        : logs/fix/emu44_20260929.log
  engine log     : logs/engine/orderecho_20260929.log
  Ctrl+C to log out; Ctrl+C again to exit at once.
20260929-06:14:56.369 INFO    session  engine  Startup: version=0.1.0 build=a1 config=config/orderecho.yaml session=emu44 FIX.4.4 AGENT->ORDERECHO@127.0.0.1:9878 evidence=data/evidence/20260929-061456.jsonl
20260929-06:14:56.369 INFO    session  emu44  Connecting to 127.0.0.1:9878
20260929-06:14:56.370 INFO    session  emu44  Connected to 127.0.0.1:9878 (local 127.0.0.1:63622)
20260929-06:14:56.481 INFO    session  emu44  connected: sending Logon
20260929-06:14:56.482 INFO    session  emu44  seqnums reset: Logon will carry 141=Y
20260929-06:14:56.482 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=ORDERECHO|34=1|52=20260929-06:14:56.481|98=0|108=30|141=Y|10=074|
20260929-06:14:56.482 INFO    session  emu44  State DISCONNECTED -> LOGON_SENT
20260929-06:14:56.492 IN   seq=1    35=A  8=FIX.4.4|9=75|35=A|49=ORDERECHO|56=AGENT|34=1|52=20260929-06:14:56.492|98=0|108=30|141=Y|10=076|
20260929-06:14:56.502 INFO    session  emu44  logon accepted: HeartBtInt=30, next_in=2 next_out=2
20260929-06:14:56.502 INFO    session  emu44  State LOGON_SENT -> ACTIVE
>>> Logged on to ORDERECHO as AGENT (HeartBtInt=30s, next_out=2 next_in=2)
20260929-06:14:56.510 OUT  seq=2    35=1  8=FIX.4.4|9=68|35=1|49=AGENT|56=ORDERECHO|34=2|52=20260929-06:14:56.510|112=TEST-1|10=096|
>>> TestRequest TEST-1 sent
20260929-06:14:56.513 IN   seq=2    35=0  8=FIX.4.4|9=68|35=0|49=ORDERECHO|56=AGENT|34=2|52=20260929-06:14:56.513|112=TEST-1|10=098|
20260929-06:14:56.517 INFO    session  emu44  testrequest answered: 112=TEST-1
>>> TestRequest TEST-1 answered in 7ms
>>> Duration 10s elapsed; logging out
20260929-06:15:06.660 OUT  seq=3    35=5  8=FIX.4.4|9=94|35=5|49=AGENT|56=ORDERECHO|34=3|52=20260929-06:15:06.660|58=OrderEcho agent: duration elapsed|10=009|
20260929-06:15:06.660 INFO    session  emu44  logout initiated: OrderEcho agent: duration elapsed
20260929-06:15:06.660 INFO    session  emu44  State ACTIVE -> LOGOUT_SENT
20260929-06:15:06.666 IN   seq=3    35=5  8=FIX.4.4|9=80|35=5|49=ORDERECHO|56=AGENT|34=3|52=20260929-06:15:06.666|58=Logout acknowledged|10=036|
20260929-06:15:06.763 INFO    session  emu44  logout confirmed: Logout acknowledged
20260929-06:15:06.763 INFO    session  emu44  Disconnecting: Logout confirmed
20260929-06:15:06.768 INFO    session  emu44  disconnected: next_out=4 next_in=4
20260929-06:15:06.768 INFO    session  emu44  State LOGOUT_SENT -> DISCONNECTED
20260929-06:15:06.768 INFO    session  emu44  Connection closed, peer=127.0.0.1:9878
20260929-06:15:06.768 INFO    session  engine  Shutdown complete
>>> Logged out cleanly (initiated by us) (their 58: "Logout acknowledged")
exit 0
```

### `bin/orderecho --config <scratch>/orderecho_strict44.yaml connect --session strict` (strict with `fix_version: FIX.4.4`)
```
OrderEcho agent 0.1.0 (a1) - session strict
  config         : /private/tmp/claude-501/-Users-dgavin-Library-CloudStorage-Dropbox-code-GitHub-OrderEcho/59c54b93-9134-4da9-bd93-54785b83b86a/scratchpad/realrun/orderecho_strict44.yaml
  route          : AGENT -> STRICTBRK  FIX.4.4  127.0.0.1:9879
  heartbeat      : 30s  reset_on_logon=true  reconnect=false
  seqnums        : data/seqnums/strict.json (next_out=1 next_in=1)
  evidence file  : data/evidence/20260929-061506.jsonl
  fix log        : logs/fix/strict_20260929.log
  engine log     : logs/engine/orderecho_20260929.log
  Ctrl+C to log out; Ctrl+C again to exit at once.
20260929-06:15:06.821 INFO    session  engine  Startup: version=0.1.0 build=a1 config=/private/tmp/claude-501/-Users-dgavin-Library-CloudStorage-Dropbox-code-GitHub-OrderEcho/59c54b93-9134-4da9-bd93-54785b83b86a/scratchpad/realrun/orderecho_strict44.yaml session=strict FIX.4.4 AGENT->STRICTBRK@127.0.0.1:9879 evidence=data/evidence/20260929-061506.jsonl
20260929-06:15:06.923 INFO    session  strict  Connecting to 127.0.0.1:9879
20260929-06:15:06.924 INFO    session  strict  Connected to 127.0.0.1:9879 (local 127.0.0.1:63628)
20260929-06:15:07.066 INFO    session  strict  connected: sending Logon
20260929-06:15:07.066 INFO    session  strict  seqnums reset: Logon will carry 141=Y
20260929-06:15:07.066 OUT  seq=1    35=A  8=FIX.4.4|9=75|35=A|49=AGENT|56=STRICTBRK|34=1|52=20260929-06:15:07.066|98=0|108=30|141=Y|10=099|
20260929-06:15:07.067 INFO    session  strict  State DISCONNECTED -> LOGON_SENT
20260929-06:15:07.069 IN   seq=1    35=5  8=FIX.4.2|9=100|35=5|49=STRICTBRK|56=AGENT|34=1|52=20260929-06:15:07.068|58=Incorrect BeginString, expected FIX.4.2|10=109|
20260929-06:15:07.069 WARNING session  strict  logon refused: counterparty answered Logon with Logout: Incorrect BeginString, expected FIX.4.2
20260929-06:15:07.069 INFO    session  strict  Disconnecting: Logon refused by counterparty: Incorrect BeginString, expected FIX.4.2
20260929-06:15:07.074 INFO    session  strict  disconnected: next_out=2 next_in=1
20260929-06:15:07.074 INFO    session  strict  State LOGON_SENT -> DISCONNECTED
20260929-06:15:07.074 INFO    session  strict  Connection closed, peer=127.0.0.1:9879
20260929-06:15:07.074 INFO    session  engine  Shutdown complete
>>> LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
orderecho: LOGON REFUSED by counterparty (Logout instead of Logon): Incorrect BeginString, expected FIX.4.2
exit 1
```

### Python viewer: `orderecho_LogView.py view --no-color logs/fix/emu42_*.log logs/fix/emu44_*.log logs/fix/strict_*.log`
```
20260929-06:14:45.553 <-- emu42              1 Logon                  141=Y 108=30
20260929-06:14:45.562 --> emu42              1 Logon                  141=Y 108=30
20260929-06:14:45.581 <-- emu42              2 Test Request           112=TEST-1
20260929-06:14:45.583 --> emu42              2 Heartbeat              112=TEST-1
20260929-06:14:55.892 <-- emu42              3 Logout                 58=OrderEcho agent: duration elapsed
20260929-06:14:56.045 --> emu42              3 Logout                 58=Logout acknowledged
20260929-06:14:56.482 <-- emu44              1 Logon                  141=Y 108=30
20260929-06:14:56.492 --> emu44              1 Logon                  141=Y 108=30
20260929-06:14:56.510 <-- emu44              2 Test Request           112=TEST-1
20260929-06:14:56.513 --> emu44              2 Heartbeat              112=TEST-1
20260929-06:15:06.660 <-- emu44              3 Logout                 58=OrderEcho agent: duration elapsed
20260929-06:15:06.666 --> emu44              3 Logout                 58=Logout acknowledged
20260929-06:15:07.066 <-- strict             1 Logon                  141=Y 108=30
20260929-06:15:07.069 --> strict             1 Logout                 58=Incorrect BeginString, expected FIX.4.2
```

### Python viewer: `orderecho_LogView.py stats --no-color logs/fix/*.log`
```
14 message(s) from 3 file(s)
  first: 2026-09-29T06:14:45.553000+00:00
  last : 2026-09-29T06:15:07.069000+00:00

By session
  emu42   6
  emu44   6
  strict  2

By message type
  5 Logout        5
  A Logon         5
  0 Heartbeat     2
  1 Test Request  2

By direction
  in   7
  out  7

Rejects by reason
  (none)

Other
  resend requests   0
  gap fills         0
  injected          0
  bad checksum      0
  bad body length   0
  unparseable lines 0
```

Afterwards I stopped the emulator with SIGINT ("Shutting down… Acceptor stopped … Shutdown complete"). `lsof` showed nothing still listening on 9878, 9879 or 8090, and no agent process is left running.

## 4. Decisions I made (conservative, logged)

1. **The session encodes its own messages.** The pure core builds the wire bytes itself (the clock is injected, so tests stay deterministic), assigns seqs and appends to the injected outbound store. The transport only writes bytes, logs and evidence. This keeps replay logic entirely inside the pure core.
2. **`reset_on_logon` lasts until a Logon is accepted.** It stays in force until one Logon reply is accepted in this process; after that, automatic reconnects never reset. Scenario 5 requires this, because the shipped config has `reset_on_logon: true` on every session. If a reset Logon is dropped before any reply, the next attempt resets again.
3. **`--reset` forces a reset for this connect.** Local seqnums go to 1/1, the store is archived, and the Logon carries `141=Y`, even when `reset_on_logon` is false. Resetting locally without telling the counterparty would guarantee a "seq too low" logout.
4. **Logon reply validation.**
   - BeginString must match our profile.
   - CompIDs must be mirrored (49 = our target, 56 = our sender).
   - 108 must be a positive integer **and equal the value we sent**.
   - Any failure gets a Logout with an explaining 58, then a disconnect.
   - 98 is not validated on the reply; the spec lists only BeginString, CompIDs and 108.
   - A reply seq that is too low gets Logout "MsgSeqNum too low, expecting X but received Y".
   - A reply carrying `141=Y` we did not ask for resets our inbound expected seq to 1 and logs a WARNING.
5. **A counterparty Logout instead of a Logon** is handled before any version or seq check, because the strict broker answers in 4.2 on its own seq. Its 58 is recorded in evidence ("logon refused: …") and in the engine log at WARNING. We disconnect without sending our own Logout, and the CLI prints `LOGON REFUSED by counterparty (Logout instead of Logon): <58>`.
6. **Any other message type as the first reply** gets a disconnect without a Logout. This mirrors the emulator's acceptor rule.
7. **Exit codes for `connect`:**
   - 0 for a clean logout in either direction.
   - 1 for everything else: refused logon, connect failure, logon timeout, connection dropped before a reply, logout timeout, or an abrupt drop after logon with reconnect off.
   - 2 for config or usage errors, including an unknown `--session`.
   - 130 when a second Ctrl+C forces the exit.
8. **Reconnect** (when enabled) happens only after an abrupt drop or a timeout. It never follows a clean logout or a refused logon. A user logout (Ctrl+C or `--duration`) turns reconnect off.
9. **`--duration` counts from the first successful logon**, not from process start. `--test-request` fires once, after the first logon only.
10. **Ctrl+C before logon** abandons the Logon and drops the connection, since there is nobody to log out from yet.
11. **Test-only injection API** (not on the CLI):
    - `SkipOutboundSeq(n)` needs n ≥ 1 and is recorded as injected evidence.
    - `SendRaw(fields)` requires 35 in the fields and rejects 8/9/10/34/49/52/56, which the session supplies. The message is sent through the normal path on the next seq, stored with `injected: true` (so a replay keeps it flagged), and gets evidence `injected: true` plus FIX log comment `# injected: SendRaw`.
12. **Carried over unchanged from the emulator:**
    - SequenceReset, TestRequest and heartbeat rules.
    - Missing-header Reject (373=1).
    - CompID mismatch while ACTIVE gets Reject 373=9, then Logout and disconnect.
    - Logon while ACTIVE gets a session Reject.
    - BeginString mismatch while ACTIVE gets Logout and disconnect.
    - The replay algorithm: admin types and missing seqs collapse into one gap fill per run; app messages are resent with `43=Y`, `122`=original 52, a new 52 and the same 34, and are never stored or counted against next_out. A stored message that no longer decodes is gap-filled, not resent broken.
    - The store is archived when its `fix_version` differs from the session's.
13. **Inbound application messages** get evidence ("application message received"), a FIX log line and a call to `app.OnAppMessage`. The A1 app is a no-op, and nothing ever answers them with a BusinessMessageReject.
14. **Evidence format.** The keys and their order are the emulator's: `ts, run_id, kind, session, seq, msg_type, raw, fields, detail, order, injected`. Two formatting differences remain: Go writes compact separators (Python writes `", "` and `": "`), and non-ASCII is written as UTF-8 rather than `\uXXXX`. Both parse identically, and the viewer reads the file.
15. **Engine log.** The logger column is always `session`, as in the emulator. Process-wide lines (startup, shutdown) put `engine` in the session column.
16. **Config.**
    - Unknown keys are rejected, naming the session and the key.
    - `reset_on_logon` is optional and defaults to false.
    - Timeouts must be > 0; `heartbeat_grace_pct` must be ≥ 0.
    - Relative storage and log paths resolve against the working directory, as in the emulator.
17. **Fixture.** The emulator's `logs/fix/` held no FIX 4.4 Heartbeat and no TestRequest of any version. So `testdata/emulator_lines.txt` has 18 lines copied from those logs (4.2 and 4.4 Logon, 4.2 Heartbeat, D, 8, Logout, ResendRequest, gap fill and a replayed 8 with 43/122). It also has 2 lines the emulator encoded itself during a scratch run: a 4.4 Heartbeat and a 4.4 TestRequest. The file comments label them. All 20 decode and re-encode byte-identical, both by re-framing the decoded fields and through `Encode`'s header layout.
18. **Timer tick** is 200 ms in the CLI and 100 ms in the interop tests. The session only needs "at least once a second".
19. **Interop harness.** Scenarios that need the injection hooks run the transport in-process; the exit-code scenarios run the real CLI binary. HTTP keep-alives are off so the emulator can shut down quickly.
20. **Leftover `EOF` file.** An empty file named `EOF` was in the repo root before I started. I left it alone.

## 5. Questions for you

1. **Is the HeartBtInt equality check too strict?** A Logon reply whose 108 differs from ours is refused. Would you rather accept it (and use which value?) or only warn?
2. **Exit code for a drop after logon.** An abrupt drop after a successful logon (with reconnect off), or a logout that times out, currently exits 1. Do you want a separate code, for example 3 for "session ended uncleanly", so A3 can tell it apart from a refused logon?
3. **Reset semantics.** Is "`reset_on_logon` applies until the first accepted Logon; reconnects never reset" what you want long-term, or should reset become a per-connect choice?
4. **Version override.** The strict-4.4 demonstration used a config copy. Should `connect` gain a `--fix-version` (or general `--set key=value`) override, or should that wait for A3/A4?
5. **Refusal handling.** When the counterparty answers our Logon with a Logout, should we send our own Logout before disconnecting? Today we don't, because the emulator hangs up at once anyway.
6. **Evidence byte-compatibility.** Do you need evidence lines to match Python's `json.dumps` byte-for-byte (spaces after `,` and `:`, `\uXXXX` escapes), or is schema-identical enough?
7. **The `EOF` file** in the repo root: keep it or delete it?
