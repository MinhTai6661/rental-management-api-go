package audit

import "strings"

// MoneyMask replaces an amount the reader may not see.
const MoneyMask = "•••"

// moneyKeys are the keys of a payload that hold an amount of money. A new action that records money under another key
// must add it here (the money tests run the real flows to catch that).
var moneyKeys = map[string]bool{
	"amount": true, "total_amount": true, "price": true, "unit_price": true, "rent_unit_price": true,
	"deposit_amount": true, "paid": true, "pending": true, "remaining": true,
	"amount_vnd": true, "total_amount_vnd": true, "deposit_amount_vnd": true, "total_vnd": true, "price_vnd": true, "unit_price_vnd": true, "deposit_vnd": true,
	"rent_unit_price_vnd": true, "paid_vnd": true, "pending_vnd": true, "remaining_vnd": true,
}

// RedactMoney hides the amounts in a changes or meta payload of the action: values under money keys, at any depth,
// become MoneyMask (a {from, to} pair keeps its shape, null stays null). The platform's own plan and subscription
// actions are left alone. The input is never changed.
func RedactMoney(action Action, payload map[string]any) map[string]any {
	if payload == nil || action.IsPlatformMoney() {
		return payload
	}
	out, _ := redactMoney(payload).(map[string]any)
	return out
}

func redactMoney(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if moneyKeys[strings.ToLower(k)] {
				out[k] = maskAmount(val)
				continue
			}
			out[k] = redactMoney(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactMoney(val)
		}
		return out
	}
	return v
}

// maskAmount hides one amount: a {from, to} pair hides each side, null stays null.
func maskAmount(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if k == "from" || k == "to" {
				out[k] = maskAmount(val)
			} else {
				out[k] = MoneyMask
			}
		}
		return out
	}
	return MoneyMask
}
