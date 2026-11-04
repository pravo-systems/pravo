package plugin

import (
	"bytes"
	"strings"
	"testing"
)

func TestMaskWriterHidesASecretInEveryForm(t *testing.T) {
	const v = `AKIA+/key"x&y`
	MaskSecret(v)
	MaskSecret("ab") // too short to mask
	var buf bytes.Buffer
	w := maskWriter{&buf}
	line := "raw " + v + " query Action=x&k=" + "AKIA%2B%2Fkey%22x%26y" + ` json "AKIA+/key\"x&y" ab` + "\n"
	n, err := w.Write([]byte(line))
	if err != nil || n != len(line) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	got := buf.String()
	for _, leak := range []string{`AKIA+/key`, "AKIA%2B%2Fkey", `key\"x`} {
		if strings.Contains(got, leak) {
			t.Errorf("log still holds %q: %s", leak, got)
		}
	}
	if !strings.Contains(got, "(secret)") || !strings.Contains(got, " ab") {
		t.Errorf("log = %s", got)
	}
}
