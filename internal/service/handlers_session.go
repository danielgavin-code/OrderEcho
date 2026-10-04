package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
	"github.com/danielgavin-code/OrderEcho/internal/order"
	"github.com/danielgavin-code/OrderEcho/internal/store"
)

var handlers = map[string]handlerFunc{
	"list_sessions":           hListSessions,
	"connect_session":         hConnect,
	"disconnect_session":      hDisconnect,
	"session_status":          hSessionStatus,
	"send_order":              hSendOrder,
	"cancel_order":            hCancelOrder,
	"replace_order":           hReplaceOrder,
	"list_orders":             hListOrders,
	"order_timeline":          hOrderTimeline,
	"recent_messages":         hRecentMessages,
	"list_cert_suites":        hListSuites,
	"list_cert_targets":       hListTargets,
	"start_cert_run":          hStartRun,
	"cert_run_status":         hRunStatus,
	"cert_run_results":        hRunResults,
	"attest_cert_case":        hAttest,
	"emulator_fill_order":     hEmuFill,
	"emulator_cancel_order":   hEmuCancel,
	"emulator_hold_order":     hEmuHold,
	"emulator_inject_next":    hEmuInject,
	"emulator_inject_seq_gap": hEmuSeqGap,
	"cert_run_report":         hRunReport,
}

// SessionInfo is one list_sessions row.
type SessionInfo struct {
	SessionID       string `json:"session_id"`
	FixVersion      string `json:"fix_version"`
	Route           string `json:"route"`
	Address         string `json:"address"`
	State           string `json:"state"`
	Connected       bool   `json:"connected"`
	External        bool   `json:"external"`
	EmulatorSession string `json:"emulator_session,omitempty"`
	CertRun         string `json:"cert_run,omitempty"`
	Orders          int    `json:"orders"`
	OpenOrders      int    `json:"open_orders"`
}

func (s *Service) sessionInfo(st *sessState) SessionInfo {
	sc := st.cfg
	info := SessionInfo{SessionID: sc.ID, FixVersion: sc.FixVersion, Route: sc.SenderCompID + " -> " + sc.TargetCompID,
		Address: sc.Addr(), State: "DISCONNECTED", External: sc.External(), CertRun: st.busy()}
	if emu, ok := s.cfg.EmulatorSession(sc.ID); ok {
		info.EmulatorSession = emu
	}
	if c := st.current(); c != nil && c.connected() {
		info.State = string(c.a.Init.Snapshot().State)
		info.Connected = true
	}
	for _, c := range st.allConns() {
		c.a.With(func(m *order.Manager) {
			for _, o := range m.Orders() {
				info.Orders++
				if !o.Terminal() {
					info.OpenOrders++
				}
			}
		})
	}
	if info.CertRun != "" {
		info.State = "CERT_RUN"
	}
	return info
}

func hListSessions(_ context.Context, s *Service, _ *call) (any, string, *APIError) {
	var rows []SessionInfo
	var parts []string
	for _, id := range s.order {
		info := s.sessionInfo(s.sessions[id])
		rows = append(rows, info)
		p := fmt.Sprintf("%s (%s %s) %s", info.SessionID, info.FixVersion, info.Route, info.State)
		if info.External {
			p += " [external]"
		}
		if info.EmulatorSession != "" {
			p += " [emulator]"
		}
		parts = append(parts, p)
	}
	return map[string]any{"sessions": rows}, fmt.Sprintf("%d session(s): %s", len(rows), strings.Join(parts, "; ")), nil
}

func hConnect(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in ConnectArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	st, e := s.session(in.SessionID)
	if e != nil {
		return nil, "", e
	}
	already := false
	if cur := st.current(); cur != nil && cur.connected() {
		already = true
	}
	cn, e := s.connect(st, in.Reset)
	if e != nil {
		return nil, "", e
	}
	snap := cn.a.Init.Snapshot()
	out := map[string]any{"session_id": in.SessionID, "state": string(snap.State), "already_connected": already,
		"next_out": snap.NextOut, "next_in": snap.NextIn, "heart_bt_int": snap.HeartBtInt, "reset": in.Reset && !already,
		"external": st.cfg.External(), "evidence_file": cn.a.Evidence.Path, "fix_log": cn.a.FixLog.PathFor(time.Now())}
	if already {
		return out, fmt.Sprintf("%s was already connected (%s, next_out=%d next_in=%d)", in.SessionID, snap.State, snap.NextOut, snap.NextIn), nil
	}
	return out, fmt.Sprintf("%s logged on to %s as %s (%s, HeartBtInt=%ds, next_out=%d next_in=%d)", in.SessionID,
		st.cfg.TargetCompID, st.cfg.SenderCompID, st.cfg.FixVersion, snap.HeartBtInt, snap.NextOut, snap.NextIn), nil
}

func hDisconnect(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in SessionArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	st, e := s.session(in.SessionID)
	if e != nil {
		return nil, "", e
	}
	st.op.Lock()
	defer st.op.Unlock()
	if run := st.busy(); run != "" {
		return nil, "", busyErr(in.SessionID, run)
	}
	cur := st.current()
	if cur == nil || !cur.connected() {
		if cur != nil {
			cur.close()
		}
		return map[string]any{"session_id": in.SessionID, "state": "DISCONNECTED", "was_connected": false},
			in.SessionID + " was not connected", nil
	}
	err := cur.live.LogoutWith("OrderEcho agent: disconnect_session")
	clean := err == nil
	detail := "logged out cleanly"
	if err != nil {
		detail = err.Error()
	}
	cur.close()
	out := map[string]any{"session_id": in.SessionID, "state": "DISCONNECTED", "was_connected": true, "clean_logout": clean, "detail": detail}
	return out, fmt.Sprintf("%s disconnected: %s", in.SessionID, detail), nil
}

func hSessionStatus(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in SessionArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	st, e := s.session(in.SessionID)
	if e != nil {
		return nil, "", e
	}
	info := s.sessionInfo(st)
	sc := st.cfg
	out := map[string]any{"session": info, "heartbeat_sec": sc.HeartbeatSec, "reset_on_logon": sc.ResetOnLogon}
	cur := st.current()
	if cur != nil {
		snap := cur.a.Init.Snapshot()
		cur.mu.Lock()
		lastIn, lastOut := cur.lastIn, cur.lastOut
		cur.mu.Unlock()
		out["next_out"], out["next_in"] = snap.NextOut, snap.NextIn
		out["heart_bt_int"] = snap.HeartBtInt
		out["pending_test_req_id"] = snap.PendingTestReqID
		out["logons"] = snap.Logons
		out["connection_since"] = cur.since.UTC().Format(time.RFC3339)
		out["last_in_at"], out["last_out_at"] = stamp(lastIn), stamp(lastOut)
		out["evidence_file"] = cur.a.Evidence.Path
		out["fix_log"] = cur.a.FixLog.PathFor(time.Now())
		if !cur.connected() {
			if r := cur.live.LastResult(); r != nil {
				out["last_disconnect"] = agent.Explain(*r, sc)
			}
		}
	} else {
		seq := store.NewFileSeqStore(s.cfg.Storage.SeqnumDir, sc.ID)
		if o, i, err := seq.Load(); err == nil {
			out["next_out"], out["next_in"] = o, i
		}
	}
	open := s.orderRows(st, "open")
	out["open_orders"] = open
	var last []string
	msgs := st.ring.snapshot()
	for i := max(0, len(msgs)-5); i < len(msgs); i++ {
		last = append(last, describeWire(msgs[i], profileFor(sc.FixVersion)))
	}
	out["last_messages"] = last
	summary := fmt.Sprintf("%s %s next_out=%v next_in=%v, %d open order(s)", in.SessionID, info.State, out["next_out"], out["next_in"], len(open))
	if info.CertRun != "" {
		summary += ", in use by cert run " + info.CertRun
	}
	if v, ok := out["last_disconnect"]; ok {
		summary += fmt.Sprintf(" (last disconnect: %v)", v)
	}
	return out, summary, nil
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func profileFor(v string) *profile.Profile {
	p, err := profile.For(v)
	if err != nil {
		return profile.FIX42
	}
	return p
}

// describeWire is one human line for a wire message.
func describeWire(w wireMsg, p *profile.Profile) string {
	m := w.Msg
	arrow := "<-"
	if w.Dir == "out" {
		arrow = "->"
	}
	mt := m.MsgType()
	line := fmt.Sprintf("%s %s %s seq=%s", stamp(w.TS), arrow, p.MsgTypeName(mt), m.Value(34))
	add := func(tag int) {
		if v, ok := m.Get(tag); ok {
			name := profile.TagName(tag)
			if vn := p.ValueName(tag, v); vn != "" {
				v += "(" + vn + ")"
			}
			line += fmt.Sprintf(" %s=%s", name, v)
		}
	}
	switch mt {
	case "8":
		for _, t := range []int{11, 37, 150, 39, 32, 31, 14, 151, 6, 103, 58} {
			add(t)
		}
	case "D", "F", "G":
		for _, t := range []int{11, 41, 55, 54, 38, 40, 44, 59} {
			add(t)
		}
	case "9":
		for _, t := range []int{11, 41, 37, 39, 434, 102, 58} {
			add(t)
		}
	case "3", "j":
		for _, t := range []int{45, 371, 372, 373, 380, 58} {
			add(t)
		}
	case "1", "0":
		add(112)
	case "A":
		add(108)
		add(141)
	case "2":
		add(7)
		add(16)
	case "4":
		add(123)
		add(36)
	case "5":
		add(58)
	}
	if m.Value(43) == "Y" {
		line += " PossDup"
	}
	return line
}

// ------------------------------------------------------------ recent_messages

// DecodedField is one field with its names.
type DecodedField struct {
	Tag     int    `json:"tag"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
	Meaning string `json:"meaning,omitempty"`
}

// DecodedMessage is one decoded wire message.
type DecodedMessage struct {
	TS          string         `json:"ts"`
	Direction   string         `json:"direction"`
	Seq         int            `json:"seq"`
	MsgType     string         `json:"msg_type"`
	MsgTypeName string         `json:"msg_type_name"`
	Summary     string         `json:"summary"`
	Fields      []DecodedField `json:"fields"`
}

func decodeMessage(w wireMsg, p *profile.Profile) DecodedMessage {
	m := w.Msg
	seq, _ := strconv.Atoi(m.Value(34))
	d := DecodedMessage{TS: stamp(w.TS), Direction: w.Dir, Seq: seq, MsgType: m.MsgType(), MsgTypeName: p.MsgTypeName(m.MsgType()),
		Summary: describeWire(w, p)}
	for _, f := range m.Fields {
		if f.Tag == 9 || f.Tag == 10 {
			continue // framing
		}
		d.Fields = append(d.Fields, DecodedField{Tag: f.Tag, Name: profile.TagName(f.Tag), Value: f.Value, Meaning: p.ValueName(f.Tag, f.Value)})
	}
	return d
}

func hRecentMessages(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in MessagesArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	st, e := s.session(in.SessionID)
	if e != nil {
		return nil, "", e
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	dir := strings.ToLower(in.Direction)
	if dir == "" {
		dir = "both"
	}
	p := profileFor(st.cfg.FixVersion)
	want := map[string]bool{}
	for _, t := range in.MsgTypes {
		t = strings.TrimSpace(t)
		code := t
		for k, name := range p.MsgTypeNames {
			if strings.EqualFold(name, t) {
				code = k
			}
		}
		want[code] = true
	}
	var picked []wireMsg
	all := st.ring.snapshot()
	for i := len(all) - 1; i >= 0 && len(picked) < limit; i-- {
		w := all[i]
		if dir != "both" && w.Dir != dir {
			continue
		}
		if len(want) > 0 && !want[w.Msg.MsgType()] {
			continue
		}
		picked = append(picked, w)
	}
	msgs := make([]DecodedMessage, 0, len(picked))
	for i := len(picked) - 1; i >= 0; i-- {
		msgs = append(msgs, decodeMessage(picked[i], p))
	}
	counts := map[string]int{}
	for _, m := range msgs {
		counts[m.MsgTypeName]++
	}
	var parts []string
	for k, n := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", n, k))
	}
	sortStrings(parts)
	summary := fmt.Sprintf("%d message(s) on %s", len(msgs), in.SessionID)
	if len(parts) > 0 {
		summary += ": " + strings.Join(parts, ", ")
	}
	if len(all) == 0 {
		summary += " (nothing sent or received since the service started; connect_session first)"
	}
	return map[string]any{"session_id": in.SessionID, "messages": msgs}, summary, nil
}

var _ = codec.Field{}
