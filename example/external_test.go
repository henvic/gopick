package example_test

import (
	"encoding/base64"
	"fmt"
	"testing"
)

func TestExternal(t *testing.T) {
	t.Log("This test belongs to package example_test.")
}

func Example() {
	fmt.Println(base64.StdEncoding.EncodeToString([]byte("Hello, Go!")))
	// Output: SGVsbG8sIEdvIQ==
}
