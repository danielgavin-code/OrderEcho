// Package profile holds everything that differs between the FIX versions the
// agent speaks. For the session layer in A1 the tag content is identical; the
// profile is still the single place version differences live (A2 adds order
// message rendering here).
package profile

import (
	"fmt"

	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"sort"
	"strings"
)

// Version strings.
const (
	FIX42Name = "FIX.4.2"
	FIX44Name = "FIX.4.4"
)

// Profile describes one FIX version.
type Profile struct {
	Name        string // config name, e.g. FIX.4.2
	Label       string // prose, e.g. FIX 4.2
	BeginString string
	// AdminTypes are session-level message types. They are never replayed on
	// a ResendRequest; runs of them collapse into gap fills.
	AdminTypes map[string]bool
	// AppTypes are application message types this version defines that we
	// know by name (used for logging; unknown types are still application
	// messages).
	AppTypes map[string]bool
	// MsgTypeNames names MsgType values for logs.
	MsgTypeNames map[string]string
	// SessionRejectReasons names SessionRejectReason (373) values.
	SessionRejectReasons map[string]string
}

// IsAdmin reports whether msgType is a session-level (admin) message.
func (p *Profile) IsAdmin(msgType string) bool { return p.AdminTypes[msgType] }

// MsgTypeName returns the human name of a MsgType, or the raw value.
func (p *Profile) MsgTypeName(msgType string) string {
	if name, ok := p.MsgTypeNames[msgType]; ok {
		return name
	}
	return msgType
}

// RejectReasonName names a SessionRejectReason (373) value.
func (p *Profile) RejectReasonName(code string) string {
	if name, ok := p.SessionRejectReasons[code]; ok {
		return name
	}
	return code
}

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

func merged(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

var adminTypes = set("0", "1", "2", "3", "4", "5", "A")

var commonNames = map[string]string{
	"0": "Heartbeat",
	"1": "TestRequest",
	"2": "ResendRequest",
	"3": "Reject",
	"4": "SequenceReset",
	"5": "Logout",
	"A": "Logon",
	"8": "ExecutionReport",
	"9": "OrderCancelReject",
	"D": "NewOrderSingle",
	"F": "OrderCancelRequest",
	"G": "OrderCancelReplaceRequest",
	"H": "OrderStatusRequest",
	"j": "BusinessMessageReject",
}

var commonRejectReasons = map[string]string{
	"0":  "Invalid tag number",
	"1":  "Required tag missing",
	"2":  "Tag not defined for this message type",
	"3":  "Undefined tag",
	"4":  "Tag specified without a value",
	"5":  "Value is incorrect (out of range) for this tag",
	"6":  "Incorrect data format for value",
	"7":  "Decryption problem",
	"8":  "Signature problem",
	"9":  "CompID problem",
	"10": "SendingTime accuracy problem",
	"11": "Invalid MsgType",
}

// FIX42 is the FIX 4.2 profile.
var FIX42 = &Profile{
	Name:                 FIX42Name,
	Label:                "FIX 4.2",
	BeginString:          FIX42Name,
	AdminTypes:           adminTypes,
	AppTypes:             set("8", "9", "D", "F", "G", "H", "j"),
	MsgTypeNames:         commonNames,
	SessionRejectReasons: commonRejectReasons,
}

// FIX44 is the FIX 4.4 profile.
var FIX44 = &Profile{
	Name:         FIX44Name,
	Label:        "FIX 4.4",
	BeginString:  FIX44Name,
	AdminTypes:   adminTypes,
	AppTypes:     set("8", "9", "D", "F", "G", "H", "j", "AE", "AR"),
	MsgTypeNames: merged(commonNames, map[string]string{"AE": "TradeCaptureReport", "AR": "TradeCaptureReportAck"}),
	SessionRejectReasons: merged(commonRejectReasons, map[string]string{
		"12": "XML Validation error",
		"13": "Tag appears more than once",
		"14": "Tag specified out of required order",
		"15": "Repeating group fields out of order",
		"16": "Incorrect NumInGroup count for repeating group",
		"17": "Non-data value includes field delimiter",
		"99": "Other",
	}),
}

var profiles = map[string]*Profile{FIX42Name: FIX42, FIX44Name: FIX44}

// Supported lists the version names we have profiles for, sorted.
func Supported() []string {
	out := make([]string, 0, len(profiles))
	for name := range profiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// For returns the profile for a fix_version.
func For(name string) (*Profile, error) {
	if p, ok := profiles[name]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("unsupported FIX version %q; supported: %s", name, strings.Join(Supported(), ", "))
}

// ------------------------------------------------------------ orders (A2)

// NewOrder is a version-neutral NewOrderSingle.
type NewOrder struct {
	ClOrdID      string
	Symbol       string
	Side         string // FIX value, e.g. "1"
	OrdType      string // "1" market, "2" limit
	OrderQty     string
	Price        string // limit only
	TimeInForce  string // "" = omit
	Account      string // "" = omit
	TransactTime string
}

// CancelRequest is a version-neutral OrderCancelRequest.
type CancelRequest struct {
	OrigClOrdID  string
	ClOrdID      string
	OrderID      string // "" = unknown, omitted
	Symbol       string
	Side         string
	TransactTime string
	OrderQty     string
}

// ReplaceRequest is a version-neutral OrderCancelReplaceRequest.
type ReplaceRequest struct {
	OrigClOrdID  string
	ClOrdID      string
	OrderID      string
	Symbol       string
	Side         string
	TransactTime string
	OrderQty     string
	OrdType      string
	Price        string
}

// Order message field layout options.
type OrderOptions struct {
	// IncludeHandlInst adds 21=1 to D and G in FIX 4.4 (always present in 4.2).
	IncludeHandlInst bool
}

func (p *Profile) handlInst(o OrderOptions) bool {
	return p.Name == FIX42Name || o.IncludeHandlInst
}

// RenderNewOrder lays out a D body: 11, 21, 55, 54, 60, 38, 40, [44], [59], [1].
func (p *Profile) RenderNewOrder(o NewOrder, opt OrderOptions) []codec.Field {
	body := []codec.Field{codec.F(11, o.ClOrdID)}
	if p.handlInst(opt) {
		body = append(body, codec.F(21, "1"))
	}
	body = append(body,
		codec.F(55, o.Symbol), codec.F(54, o.Side), codec.F(60, o.TransactTime),
		codec.F(38, o.OrderQty), codec.F(40, o.OrdType))
	if o.OrdType == OrdTypeLimit {
		body = append(body, codec.F(44, o.Price))
	}
	if o.TimeInForce != "" {
		body = append(body, codec.F(59, o.TimeInForce))
	}
	if o.Account != "" {
		body = append(body, codec.F(1, o.Account))
	}
	return body
}

// RenderCancel lays out an F body: 41, 11, [37], 55, 54, 60, 38.
func (p *Profile) RenderCancel(c CancelRequest) []codec.Field {
	body := []codec.Field{codec.F(41, c.OrigClOrdID), codec.F(11, c.ClOrdID)}
	if c.OrderID != "" {
		body = append(body, codec.F(37, c.OrderID))
	}
	return append(body, codec.F(55, c.Symbol), codec.F(54, c.Side), codec.F(60, c.TransactTime), codec.F(38, c.OrderQty))
}

// RenderReplace lays out a G body: 41, 11, [37], 21, 55, 54, 60, 38, 40, [44].
func (p *Profile) RenderReplace(r ReplaceRequest, opt OrderOptions) []codec.Field {
	body := []codec.Field{codec.F(41, r.OrigClOrdID), codec.F(11, r.ClOrdID)}
	if r.OrderID != "" {
		body = append(body, codec.F(37, r.OrderID))
	}
	if p.handlInst(opt) {
		body = append(body, codec.F(21, "1"))
	}
	body = append(body, codec.F(55, r.Symbol), codec.F(54, r.Side), codec.F(60, r.TransactTime),
		codec.F(38, r.OrderQty), codec.F(40, r.OrdType))
	if r.OrdType == OrdTypeLimit {
		body = append(body, codec.F(44, r.Price))
	}
	return body
}

// FIX values used by the order layer.
const (
	OrdTypeMarket = "1"
	OrdTypeLimit  = "2"
)

// ExecTypeName names an ExecType (150) value for this version.
func (p *Profile) ExecTypeName(v string) string {
	names := map[string]string{"0": "New", "1": "Partial fill", "2": "Fill", "3": "Done for day", "4": "Canceled",
		"5": "Replaced", "6": "Pending Cancel", "7": "Stopped", "8": "Rejected", "9": "Suspended",
		"A": "Pending New", "B": "Calculated", "C": "Expired", "D": "Restated", "E": "Pending Replace"}
	if p.Name == FIX44Name {
		names["F"] = "Trade"
		names["G"] = "Trade Correct"
		names["H"] = "Trade Cancel"
		names["I"] = "Order Status"
	}
	if n, ok := names[v]; ok {
		return n
	}
	return v
}

// OrdStatusName names an OrdStatus (39) value.
func (p *Profile) OrdStatusName(v string) string {
	names := map[string]string{"0": "New", "1": "Partially filled", "2": "Filled", "3": "Done for day", "4": "Canceled",
		"5": "Replaced", "6": "Pending Cancel", "7": "Stopped", "8": "Rejected", "9": "Suspended",
		"A": "Pending New", "B": "Calculated", "C": "Expired", "D": "Accepted for bidding", "E": "Pending Replace"}
	if n, ok := names[v]; ok {
		return n
	}
	return v
}
