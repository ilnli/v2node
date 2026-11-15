package audit

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestRuleReasonRemainsAcceptedByPanel(t *testing.T) {
	// Control characters can occur in configured regular expressions; the
	// descriptive copy must not cause the entire audit batch to be rejected.
	tag := BlockRuleTag("node/with/slash", 3, "域名规则：regexp:a\nb\t"+strings.Repeat("界", 200))
	reason := BlockReasonFromRuleTag(tag)
	if reason == "" || len(reason) > 512 || !utf8.ValidString(reason) || strings.ContainsFunc(reason, unicode.IsControl) {
		t.Fatalf("unsafe reason: %q", reason)
	}
	if !strings.Contains(reason, "regexp:a b ") || !strings.HasSuffix(reason, "…") {
		t.Fatalf("reason lost its rule or truncation marker: %q", reason)
	}
	if got := BlockReasonFromRuleTag("forwarding/custom-route"); got != "" {
		t.Fatalf("forwarding tag became a block reason: %q", got)
	}
	if _, ok := (Record{UserID: 1, NodeID: 1, Timestamp: 1, Destination: "example.com", Port: 443, Network: "tcp", BlockReason: string([]byte{0xff})}).Clone(); ok {
		t.Fatal("invalid UTF-8 reason accepted")
	}
}
