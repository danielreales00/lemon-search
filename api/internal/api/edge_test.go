package api

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// errUnreachable is wired into a repo whose Search must never be called on the
// blank-query short-circuit path; if it surfaces, the handler reached retrieval
// when it should not have.
var errUnreachable = errors.New("repo reached on blank query")

// degenerateQueries are the hostile / malformed q values the handler must never
// 5xx or panic on. The fake repo echoes a fixed candidate set, so retrieval
// itself cannot fail; the contract under test is purely the handler's input
// handling and the ranker composing finite scores for whatever comes back.
var degenerateQueries = []struct {
	name string
	q    string
}{
	{"single char", "a"},
	{"single digit", "7"},
	{"whitespace only", "   \t  "},
	{"newlines only", "\n\n\n"},
	{"punctuation only", "!@#$%^&*()_+-=[]{}|;:'\",.<>/?"},
	{"emoji", "🍋🍕☕️🌮"},
	{"mixed emoji and text", "best 🍕 near me 🔥"},
	{"accented cafe", "café"},
	{"accented nino", "niño"},
	{"accented mixed", "crème brûlée à la mode"},
	{"cjk", "拉麵 寿司 카페"},
	{"sql injection select", "'; DROP TABLE businesses;--"},
	{"sql injection or", "' OR '1'='1"},
	{"sql injection union", "x UNION SELECT * FROM businesses"},
	{"sql comment", "coffee /* comment */ shop"},
	{"angle brackets", "<script>alert(1)</script>"},
	{"percent and ampersand", "a%20b & c=d"},
	{"leading trailing space", "   coffee   "},
	{"repeated spaces", "coffee     shop     near"},
	{"tab separated", "coffee\tshop\tnear"},
	{"control chars", "a\x00b\x01c\x1f"},
	{"very long query", strings.Repeat("coffee ", 720)}, // ~5040 chars
	{"very long single token", strings.Repeat("z", 5000)},
	{"only apostrophes", "''''"},
	{"unicode replacement char", "��"},
}

// okOrClientError asserts the response is a non-5xx with a body the handler is
// contracted to produce. A 200 must carry a non-nil results array; a 4xx must
// carry the error envelope. Anything else (notably 5xx or an undecodable body)
// is a robustness bug in production code.
func okOrClientError(t *testing.T, h http.Handler, target string) {
	t.Helper()
	rec := doGet(t, h, target)
	if rec.Code >= 500 {
		t.Fatalf("GET %q = %d (5xx); body=%q", target, rec.Code, rec.Body.String())
	}
	switch {
	case rec.Code == http.StatusOK:
		sr := decodeSearch(t, rec)
		if sr.Results == nil {
			t.Fatalf("GET %q: 200 with null results array; body=%q", target, rec.Body.String())
		}
	case rec.Code >= 400:
		if body := decodeBody(t, rec); body["error"] == "" {
			t.Fatalf("GET %q: %d without an error message; body=%q", target, rec.Code, rec.Body.String())
		}
	default:
		t.Fatalf("GET %q: unexpected status %d; body=%q", target, rec.Code, rec.Body.String())
	}
}

// TestSearchDegenerateQueriesNeverError drives every hostile q through the
// configured handler and asserts no 5xx / panic and a well-formed body.
func TestSearchDegenerateQueriesNeverError(t *testing.T) {
	repo := fakeRepo{candidates: []domain.Candidate{openCandidate("Joe's Coffee"), openCandidate("Bean There")}}
	h := newSearchServer(t, repo, loadTestConfig(t))
	for _, tc := range degenerateQueries {
		t.Run(tc.name, func(t *testing.T) {
			target := "/search?q=" + url.QueryEscape(tc.q)
			okOrClientError(t, h, target)
		})
	}
}

// TestSearchDegenerateQueriesEmptyRepo repeats the sweep with a repo that
// returns no candidates: the empty-recall path must also stay 2xx/4xx and
// always emit a non-nil results array.
func TestSearchDegenerateQueriesEmptyRepo(t *testing.T) {
	h := newSearchServer(t, fakeRepo{}, loadTestConfig(t))
	for _, tc := range degenerateQueries {
		t.Run(tc.name, func(t *testing.T) {
			okOrClientError(t, h, "/search?q="+url.QueryEscape(tc.q))
		})
	}
}

// TestSearchDegenerateQueriesWithPin runs the sweep with an exact-name pin so
// the +Inf pin score path (mapped to 1.0 by toResult) is exercised under
// hostile input and never surfaces a non-finite score over the wire.
func TestSearchDegenerateQueriesWithPin(t *testing.T) {
	pin := openCandidate("Pinned Place")
	repo := fakeRepo{candidates: []domain.Candidate{openCandidate("Other")}, pin: &pin}
	h := newSearchServer(t, repo, loadTestConfig(t))
	for _, tc := range degenerateQueries {
		t.Run(tc.name, func(t *testing.T) {
			rec := doGet(t, h, "/search?q="+url.QueryEscape(tc.q))
			if rec.Code >= 500 {
				t.Fatalf("GET q=%q = %d (5xx); body=%q", tc.q, rec.Code, rec.Body.String())
			}
			if rec.Code != http.StatusOK {
				return
			}
			for _, r := range decodeSearch(t, rec).Results {
				if math.IsNaN(r.Score) || math.IsInf(r.Score, 0) {
					t.Fatalf("GET q=%q: non-finite score %v for %q", tc.q, r.Score, r.Name)
				}
			}
		})
	}
}

// TestSearchEmptyAndWhitespaceQueriesShortCircuit confirms blank-after-trim
// queries return 200 with an empty (non-nil) results array rather than reaching
// retrieval.
func TestSearchEmptyAndWhitespaceQueriesShortCircuit(t *testing.T) {
	h := newSearchServer(t, fakeRepo{searchErr: errUnreachable}, loadTestConfig(t))
	for _, q := range []string{"", " ", "\t", "\n", "   \t \n "} {
		rec := doGet(t, h, "/search?q="+url.QueryEscape(q))
		if rec.Code != http.StatusOK {
			t.Fatalf("blank q=%q: status=%d, want 200", q, rec.Code)
		}
		if sr := decodeSearch(t, rec); len(sr.Results) != 0 {
			t.Fatalf("blank q=%q: want 0 results, got %d", q, len(sr.Results))
		}
	}
}

// TestSearchMissingQueryParam confirms a request with no q at all behaves like
// an empty query (200, empty results), not a panic.
func TestSearchMissingQueryParam(t *testing.T) {
	h := newSearchServer(t, fakeRepo{}, loadTestConfig(t))
	rec := doGet(t, h, "/search")
	if rec.Code != http.StatusOK {
		t.Fatalf("missing q: status=%d, want 200", rec.Code)
	}
	if sr := decodeSearch(t, rec); sr.Results == nil {
		t.Fatalf("missing q: results must be a non-nil array")
	}
}
