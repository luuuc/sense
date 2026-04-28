package sqlite

import "testing"

func TestDecompose(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"CopyPaymentLink", "copy payment link"},
		{"handle_payment_error", "handle payment error"},
		{"HTTPSHandler", "https handler"},
		{"getHTTPSURL", "get https url"},
		{"XMLParser", "xml parser"},
		{"getURL", "get url"},
		{"parseJSON", "parse json"},
		{"simple", "simple"},
		{"A", "a"},
		{"", ""},
		{"already_lower", "already lower"},
		{"MixedSnake_CamelCase", "mixed snake camel case"},
		{"search.Engine", "search engine"},
		{"Checkout::Order", "checkout order"},
		{"io_timeout_handler", "io timeout handler"},
		{"APIClient", "api client"},
		{"newHTTPClient", "new http client"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Decompose(tt.input)
			if got != tt.want {
				t.Errorf("Decompose(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"HTTPSHandler", []string{"HTTPS", "Handler"}},
		{"CopyPaymentLink", []string{"Copy", "Payment", "Link"}},
		{"getURL", []string{"get", "URL"}},
		{"simple", []string{"simple"}},
		{"X", []string{"X"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitCamelCase(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitCamelCase(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitCamelCase(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSplitAcronymRun(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"HTTPSURL", []string{"HTTPS", "URL"}},
		{"APIDB", []string{"API", "DB"}},
		{"JSONAPI", []string{"JSON", "API"}},
		{"UNKNOWNXYZ", []string{"UNKNOWNXYZ"}},
		{"HTTPID", []string{"HTTP", "ID"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitAcronymRun(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitAcronymRun(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitAcronymRun(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}
