package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
	"github.com/danielgavin-code/OrderEcho/internal/fix/transport"
)

func TestSessionExitCodes(t *testing.T) {
	sc := config.Session{ID: "x", Host: "h", Port: 1, SenderCompID: "A", TargetCompID: "B", FixVersion: "FIX.4.2"}
	cases := []struct {
		name string
		res  transport.Result
		code int
		text string
	}{
		{"clean us", transport.Result{Logons: 1, Outcome: session.Outcome{LoggedOn: true, CleanLogout: true, LogoutByUs: true}}, ExitOK, "initiated by us"},
		{"clean them", transport.Result{Logons: 1, Outcome: session.Outcome{LoggedOn: true, CleanLogout: true}}, ExitOK, "initiated by counterparty"},
		{"refused", transport.Result{Connects: 1, Outcome: session.Outcome{Refused: true, RefusalText: "Incorrect BeginString, expected FIX.4.2"}}, ExitLogonFailed, "LOGON REFUSED"},
		{"dial", transport.Result{DialErr: errors.New("refused")}, ExitLogonFailed, "cannot connect"},
		{"logon timeout", transport.Result{Connects: 1, Outcome: session.Outcome{DisconnectCause: "Logon timeout"}}, ExitLogonFailed, "no Logon reply"},
		{"dropped before logon", transport.Result{Connects: 1}, ExitLogonFailed, "closed the connection without answering"},
		{"dropped after logon", transport.Result{Connects: 1, Logons: 1, Outcome: session.Outcome{LoggedOn: true}}, ExitDropped, "DROPPED after logon"},
		{"testrequest timeout", transport.Result{Connects: 1, Logons: 1, Outcome: session.Outcome{LoggedOn: true, DisconnectCause: "TestRequest timeout"}}, ExitDropped, "TestRequest timeout"},
		{"logout timeout", transport.Result{Connects: 1, Logons: 1, Outcome: session.Outcome{LoggedOn: true, LogoutByUs: true, DisconnectCause: "Logout timeout"}}, ExitLogoutTimeout, "LOGOUT TIMEOUT"},
		{"reconnect never got back", transport.Result{Connects: 2, Logons: 1}, ExitDropped, ""},
	}
	for _, c := range cases {
		if got := SessionExitCode(c.res); got != c.code {
			t.Fatalf("%s: exit %d want %d", c.name, got, c.code)
		}
		if c.text != "" && !strings.Contains(Explain(c.res, sc), c.text) {
			t.Fatalf("%s: %q lacks %q", c.name, Explain(c.res, sc), c.text)
		}
	}
	// The documented numbers never move.
	if ExitOK != 0 || ExitLogonFailed != 1 || ExitConfig != 2 || ExitDropped != 3 || ExitLogoutTimeout != 4 || ExitChecksFail != 5 || ExitOrderTimeout != 6 {
		t.Fatal("exit code values changed")
	}
}
