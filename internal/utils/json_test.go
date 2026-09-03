package utils

import "testing"

func TestDecodeJSONStrict(t *testing.T) {
	for _, test := range []struct {
		name, input string
		wantError   bool
	}{
		{"valid", `{"name":"web","target":{"namespace":"default"}}`, false},
		{"trailing whitespace", "{\"name\":\"web\"} \n\t", false},
		{"unknown field", `{"nmae":"web"}`, true},
		{"nested unknown field", `{"target":{"namespce":"default"}}`, true},
		{"wrong type", `{"name":1}`, true},
		{"invalid JSON", `{"name":`, true},
		{"empty", ``, true},
		{"multiple values", `{"name":"web"} {}`, true},
		{"trailing garbage", `{"name":"web"} garbage`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var dst struct {
				Name   string `json:"name"`
				Target struct {
					Namespace string `json:"namespace"`
				} `json:"target"`
			}
			err := DecodeJSONStrict(test.input, &dst)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError = %v", err, test.wantError)
			}
			if !test.wantError && dst.Name != "web" {
				t.Fatalf("decoded name = %q", dst.Name)
			}
		})
	}
}
