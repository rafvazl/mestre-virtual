package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI(t *testing.T) {
	content := filepath.Join("..", "..", "content")
	data := t.TempDir()
	var out bytes.Buffer
	steps := [][]string{
		{"content", "-content", content},
		{"character", "create", "-content", content, "-data", data, "-id", "aria", "-name", "Aria", "-class", "guerreiro"},
		{"character", "list", "-data", data},
		{"ask", "-llm", "routing", "abro", "a", "porta"},
	}
	for _, s := range steps {
		if err := run(s, &out); err != nil {
			t.Fatalf("%v: %v", s, err)
		}
	}
	for _, want := range []string{"guerreiro", "criado Aria", "aria\tAria", "[local/echo]", "abro a porta"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("saída sem %q:\n%s", want, out.String())
		}
	}
	if err := run([]string{"character", "create", "-content", content, "-data", data, "-id", "aria", "-name", "X", "-class", "guerreiro"}, &out); err == nil {
		t.Error("duplicado deveria falhar")
	}
	if err := run([]string{"ask", "-llm", "remote", "oi"}, &out); err == nil {
		t.Error("remote stub deveria falhar")
	}
}
