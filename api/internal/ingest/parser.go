// Package ingest streams the malformed businesses-*.json data file and loads
// it into Postgres. The file is pretty-printed objects separated by "}\n{"
// (not "},\n{"), wrapped in a leading "[" and trailing "]", so a single
// json.Unmarshal over the whole file fails at the first object boundary.
package ingest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Parser yields one balanced JSON object at a time from a stream of
// pretty-printed objects. It tolerates the "}\n{" separator malformation by
// tracking brace depth itself rather than relying on the array's commas.
type Parser struct {
	r        *bufio.Reader
	depth    int
	inString bool
	escape   bool
	buf      []byte
}

// New returns a Parser reading from r.
func New(r io.Reader) *Parser {
	return &Parser{r: bufio.NewReader(r)}
}

// Next returns the next balanced JSON object, or io.EOF once the stream is
// exhausted. Structural bytes between objects (the leading "[", inter-object
// commas and whitespace, and the trailing "]") are skipped. An object that
// ends before its braces balance yields an "unexpected EOF" error rather than
// a partial value.
func (p *Parser) Next() (json.RawMessage, error) {
	if err := p.skipToObject(); err != nil {
		return nil, err
	}

	p.reset()
	for {
		b, err := p.r.ReadByte()
		if err != nil {
			return nil, p.readErr(err)
		}
		if done := p.consume(b); done {
			out := make([]byte, len(p.buf))
			copy(out, p.buf)
			return out, nil
		}
	}
}

// skipToObject advances past structural bytes until the next '{'. The '{' is
// unread so the main loop sees it at depth 0.
func (p *Parser) skipToObject() error {
	for {
		b, err := p.r.ReadByte()
		if err != nil {
			return p.skipErr(err)
		}
		if b == '{' {
			if uErr := p.r.UnreadByte(); uErr != nil {
				return fmt.Errorf("unreading object start: %w", uErr)
			}
			return nil
		}
		if !isStructural(b) {
			return fmt.Errorf("unexpected byte %q before object start", b)
		}
	}
}

// consume folds one byte into the in-progress object and reports whether the
// object just closed (depth returned to 0). The in-string and escape flags
// ensure braces and quotes inside string values do not move the depth.
func (p *Parser) consume(b byte) bool {
	p.buf = append(p.buf, b)

	if p.escape {
		p.escape = false
		return false
	}
	switch {
	case b == '\\' && p.inString:
		p.escape = true
	case b == '"':
		p.inString = !p.inString
	case p.inString:
		// braces inside strings are literal text
	case b == '{':
		p.depth++
	case b == '}':
		p.depth--
		return p.depth == 0
	}
	return false
}

func (p *Parser) reset() {
	p.depth = 0
	p.inString = false
	p.escape = false
	p.buf = p.buf[:0]
}

// skipErr maps a read error encountered while hunting for the next object.
// A clean EOF here means the stream is exhausted.
func (p *Parser) skipErr(err error) error {
	if errors.Is(err, io.EOF) {
		return io.EOF
	}
	return fmt.Errorf("scanning for object start: %w", err)
}

// readErr maps a read error encountered mid-object. EOF here is unexpected:
// the object never balanced.
func (p *Parser) readErr(err error) error {
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("reading object body: %w", io.ErrUnexpectedEOF)
	}
	return fmt.Errorf("reading object body: %w", err)
}

func isStructural(b byte) bool {
	switch b {
	case '[', ']', ',', ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}
