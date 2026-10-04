package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/order"
)

// The emulator tools act on the counterparty emulator through its control
// API (emulator.control_api), never on a real venue.

func (s *Service) emulatorAPI() (string, *APIError) {
	if !s.EmulatorToolsAvailable() {
		return "", apiErr(409, "no_control_api", "configure emulator.control_api and emulator.sessions in the agent config to use the emulator tools", "no emulator control API is configured")
	}
	return s.cfg.Emulator.ControlAPI, nil
}

// emuCall POSTs body to the control API and returns its JSON answer.
func (s *Service) emuCall(ctx context.Context, path string, body any) (map[string]any, *APIError) {
	base, e := s.emulatorAPI()
	if e != nil {
		return nil, e
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(data))
	if err != nil {
		return nil, apiErr(500, "emulator_error", "", "%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, apiErr(502, "emulator_unreachable", fmt.Sprintf("is the emulator running with its control API on %s?", base), "POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK {
		code, _ := out["error"].(string)
		detail, _ := out["detail"].(string)
		if detail == "" {
			detail = strings.TrimSpace(string(raw))
		}
		hint := "check the order with list_orders (order_id is the emulator's OrderID, tag 37) and its state"
		switch code {
		case "not_found":
			hint = "use the OrderID (tag 37) that send_order or list_orders returned, not the ClOrdID"
		case "session_not_active":
			hint = "the emulator's session is not logged on; connect_session first"
		case "conflict":
			hint = "the order is already closed on the emulator (filled or canceled); list_orders shows its state"
		}
		if code == "" {
			code = "http_" + strconv.Itoa(resp.StatusCode)
		}
		return nil, apiErr(resp.StatusCode, "emulator_"+code, hint, "emulator refused %s: %s", path, detail)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// ourView finds our side's order with OrderID id and waits (briefly) until
// it has seen more than before reports.
func (s *Service) ourView(ctx context.Context, orderID string, before map[string]int) (map[string]any, string) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, id := range s.order {
			st := s.sessions[id]
			for _, c := range st.allConns() {
				var o *order.Order
				var reports int
				c.a.With(func(m *order.Manager) {
					for _, x := range m.Orders() {
						if x.OrderID == orderID {
							o, reports = x, x.Reports
						}
					}
				})
				if o == nil {
					continue
				}
				if reports > before[orderID] || time.Now().After(deadline) || ctx.Err() != nil {
					r := orderResult(id, c.a, o)
					return map[string]any{"session_id": id, "order": r}, r.oneLine()
				}
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, "our side has no order with OrderID " + orderID
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *Service) reportsBefore(orderID string) map[string]int {
	out := map[string]int{}
	for _, id := range s.order {
		for _, c := range s.sessions[id].allConns() {
			c.a.With(func(m *order.Manager) {
				for _, x := range m.Orders() {
					if x.OrderID == orderID {
						out[orderID] = x.Reports
					}
				}
			})
		}
	}
	return out
}

func (s *Service) emuOrderAction(ctx context.Context, orderID, action string, body any) (any, string, *APIError) {
	if strings.TrimSpace(orderID) == "" {
		return nil, "", apiErr(400, "invalid_arguments", "order_id is the emulator's OrderID (tag 37)", "order_id is required")
	}
	before := s.reportsBefore(orderID)
	out, e := s.emuCall(ctx, "/orders/"+url.PathEscape(orderID)+"/"+action, body)
	if e != nil {
		return nil, "", e
	}
	view, line := s.ourView(ctx, orderID, before)
	res := map[string]any{"acted_on": "counterparty emulator (not a real venue)", "order_id": orderID, "emulator": out, "our_view": view}
	emuState := ""
	if o, ok := out["order"].(map[string]any); ok {
		if st, ok := o["status"].(string); ok {
			emuState = st
		} else if st, ok := o["state"].(string); ok {
			emuState = st
		}
	}
	summary := fmt.Sprintf("emulator %s %s done", action, orderID)
	if emuState != "" {
		summary += " (emulator: " + emuState + ")"
	}
	return res, summary + "; our side: " + line, nil
}

func hEmuFill(ctx context.Context, s *Service, c *call) (any, string, *APIError) {
	var in EmuFillArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	qty, err := strconv.Atoi(numText(in.Qty))
	if err != nil || qty <= 0 {
		return nil, "", apiErr(400, "invalid_arguments", "qty is a positive whole number, at most the order's leaves", "qty must be a positive whole number, got %v", in.Qty)
	}
	body := map[string]any{"qty": qty}
	if px := numText(in.Price); px != "" {
		body["price"] = px
	}
	return s.emuOrderAction(ctx, in.OrderID, "fill", body)
}

func hEmuCancel(ctx context.Context, s *Service, c *call) (any, string, *APIError) {
	var in EmuOrderArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	return s.emuOrderAction(ctx, in.OrderID, "cancel", map[string]any{})
}

func hEmuHold(ctx context.Context, s *Service, c *call) (any, string, *APIError) {
	var in EmuOrderArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	return s.emuOrderAction(ctx, in.OrderID, "hold", map[string]any{})
}

// emuSession maps our session id (or the emulator's own) to the emulator's.
func (s *Service) emuSession(id string) (string, *APIError) {
	if v, ok := s.cfg.EmulatorSession(id); ok {
		return v, nil
	}
	for _, v := range s.cfg.Emulator.Sessions {
		if v == id && s.cfg.Emulator.ControlAPI != "" {
			return v, nil
		}
	}
	var known []string
	for k, v := range s.cfg.Emulator.Sessions {
		known = append(known, k+" ("+v+")")
	}
	sortStrings(known)
	return "", apiErr(404, "unknown_session", "sessions backed by the emulator: "+strings.Join(known, ", "), "%q is not a session whose counterparty is the emulator", id)
}

func hEmuSeqGap(ctx context.Context, s *Service, c *call) (any, string, *APIError) {
	var in EmuSeqGapArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	emu, e := s.emuSession(in.Session)
	if e != nil {
		return nil, "", e
	}
	out, e := s.emuCall(ctx, "/sessions/"+url.PathEscape(emu)+"/inject/seq-gap", map[string]any{"skip": in.Skip})
	if e != nil {
		return nil, "", e
	}
	res := map[string]any{"acted_on": "counterparty emulator (not a real venue)", "emulator_session": emu, "emulator": out}
	return res, fmt.Sprintf("emulator %s skipped %d outbound sequence number(s); the agent should ask for a resend", emu, in.Skip), nil
}

func hEmuInject(ctx context.Context, s *Service, c *call) (any, string, *APIError) {
	var in EmuInjectArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	emu, e := s.emuSession(in.Session)
	if e != nil {
		return nil, "", e
	}
	if len(in.Set) == 0 && len(in.Remove) == 0 {
		return nil, "", apiErr(400, "invalid_arguments", "give set (tag -> value) and/or remove (tags)", "emulator_inject_next changes nothing")
	}
	var remove []string
	for _, r := range in.Remove {
		t := numText(r)
		if _, err := strconv.Atoi(t); err != nil {
			return nil, "", apiErr(400, "invalid_arguments", "remove lists tag numbers, e.g. [151]", "not a tag number: %v", r)
		}
		remove = append(remove, t)
	}
	for k := range in.Set {
		if _, err := strconv.Atoi(k); err != nil {
			return nil, "", apiErr(400, "invalid_arguments", "set maps tag numbers to values, e.g. {\"58\": \"hello\"}", "not a tag number: %q", k)
		}
	}
	body := map[string]any{}
	if in.MsgType != "" {
		body["msg_type"] = in.MsgType
	}
	if len(in.Set) > 0 {
		body["set"] = in.Set
	}
	if len(remove) > 0 {
		body["remove"] = remove
	}
	out, e := s.emuCall(ctx, "/sessions/"+url.PathEscape(emu)+"/inject/next", body)
	if e != nil {
		return nil, "", e
	}
	res := map[string]any{"acted_on": "counterparty emulator (not a real venue)", "emulator_session": emu, "emulator": out}
	what := "the next message"
	if in.MsgType != "" {
		what = "the next 35=" + in.MsgType
	}
	return res, fmt.Sprintf("emulator %s will alter %s it sends (set %v, remove %v)", emu, what, in.Set, remove), nil
}
