package example

import (
	"encoding/base64"
	"testing"
)

func TestInternal(t *testing.T) {
	t.Log("This test belongs to package example.")
}

// gopick lists TestSubtests, but not its nested or table-driven subtests.
// Go discovers those when the parent runs. To select one leaf:
// gopick ./example '^TestSubtests$/^base64$/^hello$'
func TestSubtests(t *testing.T) {
	t.Run("base64", func(t *testing.T) {
		for _, tc := range []struct {
			name, input, want string
		}{
			{name: "empty", input: "", want: ""},
			{name: "hello", input: "Hello, Go!", want: "SGVsbG8sIEdvIQ=="},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := base64.StdEncoding.EncodeToString([]byte(tc.input)); got != tc.want {
					t.Fatalf("encoded = %q, want %q", got, tc.want)
				}
			})
		}
	})
}

func FuzzBase64RoundTrip(f *testing.F) {
	f.Add("Hello, Go!")
	f.Add("")
	f.Add("\x00\xff")
	f.Fuzz(func(t *testing.T, input string) {
		encoded := base64.StdEncoding.EncodeToString([]byte(input))
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(decoded) != input {
			t.Fatalf("round trip = %q, want %q", decoded, input)
		}
	})
}
