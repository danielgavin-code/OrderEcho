package profile

// Tag and value names for decoded messages (A4 recent_messages): the tags
// the agent and the emulator actually use in FIX 4.2 / 4.4 order entry.

var tagNames = map[int]string{
	1: "Account", 6: "AvgPx", 7: "BeginSeqNo", 8: "BeginString", 9: "BodyLength", 10: "CheckSum",
	11: "ClOrdID", 14: "CumQty", 15: "Currency", 16: "EndSeqNo", 17: "ExecID", 18: "ExecInst",
	19: "ExecRefID", 20: "ExecTransType", 21: "HandlInst", 22: "SecurityIDSource", 31: "LastPx", 32: "LastQty",
	34: "MsgSeqNum", 35: "MsgType", 36: "NewSeqNo", 37: "OrderID", 38: "OrderQty", 39: "OrdStatus",
	40: "OrdType", 41: "OrigClOrdID", 43: "PossDupFlag", 44: "Price", 45: "RefSeqNum", 48: "SecurityID",
	49: "SenderCompID", 50: "SenderSubID", 52: "SendingTime", 54: "Side", 55: "Symbol", 56: "TargetCompID",
	57: "TargetSubID", 58: "Text", 59: "TimeInForce", 60: "TransactTime", 97: "PossResend", 98: "EncryptMethod",
	99: "StopPx", 100: "ExDestination", 102: "CxlRejReason", 103: "OrdRejReason", 108: "HeartBtInt",
	109: "ClientID", 110: "MinQty", 111: "MaxFloor", 112: "TestReqID", 122: "OrigSendingTime", 123: "GapFillFlag",
	126: "ExpireTime", 141: "ResetSeqNumFlag", 150: "ExecType", 151: "LeavesQty", 167: "SecurityType",
	207: "SecurityExchange", 371: "RefTagID", 372: "RefMsgType", 373: "SessionRejectReason",
	379: "BusinessRejectRefID", 380: "BusinessRejectReason", 432: "ExpireDate", 434: "CxlRejResponseTo",
	453: "NoPartyIDs", 448: "PartyID", 447: "PartyIDSource", 452: "PartyRole", 789: "NextExpectedMsgSeqNum",
}

// TagName names a tag ("" when unknown).
func TagName(tag int) string { return tagNames[tag] }

var (
	sideNames     = map[string]string{"1": "Buy", "2": "Sell", "3": "Buy minus", "4": "Sell plus", "5": "Sell short", "6": "Sell short exempt"}
	ordTypeNames  = map[string]string{"1": "Market", "2": "Limit", "3": "Stop", "4": "Stop limit", "5": "Market on close"}
	tifNames      = map[string]string{"0": "Day", "1": "GTC", "2": "At the opening", "3": "IOC", "4": "FOK", "5": "GTX", "6": "GTD"}
	cxlRejReasons = map[string]string{"0": "Too late to cancel", "1": "Unknown order", "2": "Broker option",
		"3": "Already pending cancel or replace", "6": "Duplicate ClOrdID"}
	ordRejReasons = map[string]string{"0": "Broker option", "1": "Unknown symbol", "2": "Exchange closed",
		"3": "Order exceeds limit", "4": "Too late to enter", "5": "Unknown order", "6": "Duplicate order",
		"7": "Duplicate of a verbally communicated order", "8": "Stale order", "11": "Unsupported order characteristic", "99": "Other"}
	businessRejReasons = map[string]string{"0": "Other", "1": "Unknown ID", "2": "Unknown security",
		"3": "Unsupported message type", "4": "Application not available", "5": "Conditionally required field missing",
		"6": "Not authorized", "7": "DeliverTo firm not available"}
	cxlRejResponseTo = map[string]string{"1": "Order cancel request", "2": "Order cancel/replace request"}
	yesNo            = map[string]string{"Y": "Yes", "N": "No"}
)

// ValueName names an enumerated value of tag for this version ("" when the
// tag is not enumerated or the value unknown).
func (p *Profile) ValueName(tag int, v string) string {
	var m map[string]string
	switch tag {
	case 35:
		if n := p.MsgTypeName(v); n != v {
			return n
		}
		return ""
	case 150:
		if n := p.ExecTypeName(v); n != v {
			return n
		}
		return ""
	case 39:
		if n := p.OrdStatusName(v); n != v {
			return n
		}
		return ""
	case 373:
		if n := p.RejectReasonName(v); n != v {
			return n
		}
		return ""
	case 54:
		m = sideNames
	case 40:
		m = ordTypeNames
	case 59:
		m = tifNames
	case 102:
		m = cxlRejReasons
	case 103:
		m = ordRejReasons
	case 380:
		m = businessRejReasons
	case 434:
		m = cxlRejResponseTo
	case 43, 97, 123, 141:
		m = yesNo
	}
	return m[v]
}
