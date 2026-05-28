package ingest

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParserYieldsObjects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "well-formed array",
			input: `[{"id":1},{"id":2}]`,
			want:  []string{`{"id":1}`, `{"id":2}`},
		},
		{
			name:  "malformed separator",
			input: "[{\"id\":1}\n{\"id\":2}\n{\"id\":3}]",
			want:  []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
		},
		{
			name:  "pretty printed",
			input: "[\n  {\n    \"id\": 1\n  }\n  {\n    \"id\": 2\n  }\n]",
			want:  []string{"{\n    \"id\": 1\n  }", "{\n    \"id\": 2\n  }"},
		},
		{
			name:  "single object no array wrapper",
			input: `{"id":1,"name":"Joe"}`,
			want:  []string{`{"id":1,"name":"Joe"}`},
		},
		{
			name:  "single object in array",
			input: `[{"id":1}]`,
			want:  []string{`{"id":1}`},
		},
		{
			name:  "escaped quote in value",
			input: `[{"name":"Joe \"The Chef\" Smith"}]`,
			want:  []string{`{"name":"Joe \"The Chef\" Smith"}`},
		},
		{
			name:  "escaped backslash before quote",
			input: `[{"path":"C:\\dir\\"}{"id":2}]`,
			want:  []string{`{"path":"C:\\dir\\"}`, `{"id":2}`},
		},
		{
			name:  "braces inside string value",
			input: `[{"tpl":"a {nested} }{ brace"}{"id":2}]`,
			want:  []string{`{"tpl":"a {nested} }{ brace"}`, `{"id":2}`},
		},
		{
			name:  "deeply nested object",
			input: `[{"a":{"b":{"c":{"d":{"e":{"f":1}}}}}}]`,
			want:  []string{`{"a":{"b":{"c":{"d":{"e":{"f":1}}}}}}`},
		},
		{
			name: "nested hours malformed separator",
			input: `[{"name":"X","hours":{"mon":{"open":"9","close":"17"}}}` +
				`{"name":"Y","hours":{"tue":{"open":"8","close":"16"}}}]`,
			want: []string{
				`{"name":"X","hours":{"mon":{"open":"9","close":"17"}}}`,
				`{"name":"Y","hours":{"tue":{"open":"8","close":"16"}}}`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := drain(t, tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("object count = %d, want %d (got %q)", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("object[%d] = %q, want %q", i, got[i], w)
				}
				if !json.Valid([]byte(got[i])) {
					t.Errorf("object[%d] is not valid JSON: %q", i, got[i])
				}
			}
		})
	}
}

func TestParserEmptyArray(t *testing.T) {
	t.Parallel()

	for _, in := range []string{`[]`, "", "   \n  ", "[\n\n]"} {
		p := New(strings.NewReader(in))
		_, err := p.Next()
		if !errors.Is(err, io.EOF) {
			t.Errorf("input %q: first Next() err = %v, want io.EOF", in, err)
		}
	}
}

func TestParserEOFIsStable(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader(`[{"id":1}]`))
	if _, err := p.Next(); err != nil {
		t.Fatalf("first Next() unexpected err: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := p.Next(); !errors.Is(err, io.EOF) {
			t.Fatalf("Next() after drain (call %d) err = %v, want io.EOF", i, err)
		}
	}
}

func TestParserTruncatedInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
	}{
		{"unbalanced open brace", `[{"id":1`},
		{"unterminated string", `[{"name":"unterminated`},
		{"missing closing brace nested", `[{"a":{"b":1}`},
		{"trailing escape opens nothing", `[{"name":"x\`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := New(strings.NewReader(tc.input))
			_, err := p.Next()
			if err == nil {
				t.Fatalf("expected an error for truncated input %q", tc.input)
			}
			if errors.Is(err, io.EOF) {
				t.Errorf("truncated input should not report clean io.EOF, got %v", err)
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("want errors.Is(err, io.ErrUnexpectedEOF), got %v", err)
			}
		})
	}
}

func TestParserGarbageBeforeObject(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader(`xyz{"id":1}`))
	if _, err := p.Next(); err == nil {
		t.Fatal("expected an error for non-structural bytes before object")
	}
}

func TestParserHandlesManyObjects(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteByte('[')
	const n = 500
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte('\n') // the malformed separator: no comma
		}
		b.WriteString(`{"id":`)
		b.WriteString(itoa(i))
		b.WriteByte('}')
	}
	b.WriteByte(']')

	got := drain(t, b.String())
	if len(got) != n {
		t.Fatalf("got %d objects, want %d", len(got), n)
	}
}

// drain reads every object from input and fails the test on any non-EOF error.
func drain(t *testing.T, input string) []string {
	t.Helper()
	p := New(strings.NewReader(input))
	var out []string
	for {
		obj, err := p.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next() unexpected err: %v", err)
		}
		out = append(out, string(obj))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
