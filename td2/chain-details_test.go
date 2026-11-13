package tenderduty

import "testing"

func TestGetAltPrefix(t *testing.T) {
	tests := []struct {
		name    string
		valoper string
		want    string
		wantOk  bool
	}{
		{"crypto.org", "crocncl1qqqsyqcyq5rqwzqfpg9scrgwpugpzysn3rl9h9", "crocnclcons", true},
		{"iris override", "ival1qqqsyqcyq5rqwzqfpg9scrgwpugpzysn", "ica", true},
		{"unknown prefix", "zzz1qqqsyqcyq5rqwzqfpg9scrgwpugpzysn", "", false},
		{"no separator", "crocncl", "", false},
		{"empty", "", "", false},
		{"separator first", "1qqqsyqcyq5rq", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := altValopers.getAltPrefix(tt.valoper)
			if got != tt.want || ok != tt.wantOk {
				t.Errorf("getAltPrefix(%q) = (%q, %v), want (%q, %v)", tt.valoper, got, ok, tt.want, tt.wantOk)
			}
		})
	}
}

func TestDeriveValconsCryptoOrg(t *testing.T) {
	address := make([]byte, consensusAddressLength)
	address[19] = 1
	valoper := mustBech32(t, "crocncl", address)
	got, err := deriveValcons(valoper, address)
	if err != nil {
		t.Fatalf("deriveValcons(%s) error = %v", valoper, err)
	}
	if want := mustBech32(t, "crocnclcons", address); got != want {
		t.Errorf("deriveValcons(%s) = %s, want %s", valoper, got, want)
	}
}
