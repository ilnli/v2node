package audit

import (
	"encoding/base64"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BlockRuleTag keeps the router's required unique identity separate from the
// bounded, public description. Descriptions may themselves contain slashes.
func BlockRuleTag(node string, position int, reason string) string {
	return "audit-block/" + base64.RawURLEncoding.EncodeToString([]byte(node)) + "/" + strconv.Itoa(position) + "/" + boundedReason(reason)
}

// BlockReasonFromRuleTag only accepts descriptions generated for deny rules.
// A forwarding rule's tag is never evidence for a final-egress denial.
func BlockReasonFromRuleTag(tag string) string {
	parts := strings.SplitN(tag, "/", 4)
	if len(parts) != 4 || parts[0] != "audit-block" {
		return ""
	}
	return boundedReason(parts[3])
}

func boundedReason(reason string) string {
	reason = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, reason)
	if len(reason) <= MaxBlockReasonBytes {
		return reason
	}
	end := MaxBlockReasonBytes - len("…")
	for !utf8.RuneStart(reason[end]) {
		end--
	}
	return reason[:end] + "…"
}
