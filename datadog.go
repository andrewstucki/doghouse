package doghouse

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

//go:generate msgp
//msgp:ignore MockDatadogServer

// Span represents a single span.
type Span struct {
	Name     string             `msg:"name"`
	Service  string             `msg:"service"`
	Resource string             `msg:"resource"`
	Type     string             `msg:"type"`
	Start    int64              `msg:"start"`
	Duration int64              `msg:"duration"`
	Meta     map[string]string  `msg:"meta,omitempty"`
	Metrics  map[string]float64 `msg:"metrics,omitempty"`
	SpanID   uint64             `msg:"span_id"`
	TraceID  uint64             `msg:"trace_id"`
	ParentID uint64             `msg:"parent_id"`
	Error    int32              `msg:"error"`
}

// Trace contains a collection of associated spans.
type Trace []Span

// Batch contains a collection of traces sent in bulk to the server.
type Batch []Trace

// MockDatadogServer is a test server that collects traces sent via Datadog's tracing library.
type MockDatadogServer struct {
	server      *httptest.Server
	path        string
	spansByID   map[uint64]Span
	spansByName map[string]Span
	lock        sync.RWMutex
}

const (
	agentEnvVariable = "DD_TRACE_AGENT_URL"
	traceHeader      = "X-Datadog-Trace-Count"
	defaultTracePath = "/v0.4/traces"
)

var initialized atomic.Bool

// New creates a new MockDatadogServer. This should only be ever used as a singleton
// due to the fact that the Datadog tracer library uses global state for publishing.
func New(opts ...tracer.StartOption) *MockDatadogServer {
	if !initialized.CompareAndSwap(false, true) {
		log.Fatal("Mocking Datadog is only ever allowed once")
	}
	s := &MockDatadogServer{
		path:        defaultTracePath,
		spansByID:   make(map[uint64]Span),
		spansByName: make(map[string]Span),
	}
	s.server = httptest.NewServer(s)
	url := s.server.URL
	os.Setenv(agentEnvVariable, url)

	opts = append(opts, tracer.WithLogStartup(false), tracer.WithPartialFlushing(10))

	tracer.Start(opts...)
	return s
}

// SetTracePath changes the url path for which the mock server accepts Datadog traces.
func (s *MockDatadogServer) SetTracePath(path string) {
	s.path = path
}

// Close the underlying test server.
func (s *MockDatadogServer) Close() {
	s.server.Close()
}

// ServeHTTP is the main handler for requests from the tracing library.
func (s *MockDatadogServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)

	if r.URL.Path != s.path {
		return
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	traceCountHeader := r.Header.Get(traceHeader)
	if traceCountHeader == "" {
		log.Print("trace count not passed as a header")
		return
	}

	traceCount, err := strconv.Atoi(traceCountHeader)
	if err != nil {
		log.Printf("failed to parse trace count %+v", err)
		return
	}

	buf := &bytes.Buffer{}
	_, err = io.Copy(buf, r.Body)
	if err != nil {
		log.Printf("failed to get body %+v", err)
		return
	}

	var batch Batch
	_, err = batch.UnmarshalMsg(buf.Bytes())
	if err != nil {
		log.Printf("failed to parse trace %+v", err)
		log.Print(buf)
		return
	}

	if len(batch) != traceCount {
		log.Printf("invalid trace count %d, expected %d", len(batch), traceCount)
		return
	}

	for _, trace := range batch {
		for _, span := range trace {
			span := span
			s.spansByID[span.SpanID] = span
			s.spansByName[span.Name] = span
		}
	}
}

func (s *MockDatadogServer) spanNames() []string {
	names := []string{}
	for _, s := range s.spansByID {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

// WaitForSpan waits 10 milliseconds for the server to receive the named span with optional parent matching.
func (s *MockDatadogServer) WaitForSpan(t *testing.T, name string, parents ...string) {
	s.WaitDurationForSpan(t, 10*time.Millisecond, name, parents...)
}

// WaitDurationForSpan waits a sepecified duration for the server to receive the named span with optional parent matching.
func (s *MockDatadogServer) WaitDurationForSpan(t *testing.T, duration time.Duration, name string, parents ...string) {
	timeout := time.After(duration)
	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()

	expectation := func() bool {
		s.lock.RLock()
		defer s.lock.RUnlock()

		span, ok := s.spansByName[name]
		if !ok {
			return false
		}

		current := span
		for _, parent := range parents {
			p, ok := s.spansByID[current.ParentID]
			if !ok {
				t.Fatalf("parent span for %q not found", current.Name)
			}
			if p.Name != parent {
				t.Fatalf("parent span %q did not match expected span %q", p.Name, parent)
			}
			current = p
		}

		return true
	}

	// first check immediately
	if expectation() {
		return
	}

	for {
		select {
		case <-timeout:
			t.Fatalf("unable to find span %q in given time", name)
		case <-ticker.C:
			if expectation() {
				return
			}
		}
	}
}

// ExpectNoSpan ensures that the named span has not been received within 100 milliseconds.
func (s *MockDatadogServer) ExpectNoSpan(t *testing.T, name string) {
	s.ExpectDurationNoSpan(t, 100*time.Millisecond, name)
}

// ExpectDurationNoSpan ensures that the named span has not been received in the given duration.
func (s *MockDatadogServer) ExpectDurationNoSpan(t *testing.T, duration time.Duration, name string) {
	timeout := time.After(duration)
	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()

	expectation := func() {
		s.lock.RLock()
		defer s.lock.RUnlock()

		_, ok := s.spansByName[name]
		if ok {
			t.Fatalf("unexpected span %q found", name)
		}
	}

	// first check immediately
	expectation()

	for {
		select {
		case <-timeout:
			return
		case <-ticker.C:
			expectation()
		}
	}
}

// Expect a named span with the given optional parents to have been received.
func (s *MockDatadogServer) ExpectSpan(t *testing.T, name string, parents ...string) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	span, ok := s.spansByName[name]
	if !ok {
		t.Fatalf("span named %q not found in spans: %v", name, s.spanNames())
	}

	current := span
	for _, parent := range parents {
		p, ok := s.spansByID[current.ParentID]
		if !ok {
			t.Fatalf("parent span for %q not found", current.Name)
		}
		if p.Name != parent {
			t.Fatalf("parent span %q did not match expected span %q", p.Name, parent)
		}
		current = p
	}
}

// Expect a named span with the given verification function to exist.
func (s *MockDatadogServer) ExpectSpanFn(t *testing.T, name string, fn func(span Span) bool, msg string, args ...interface{}) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	span, ok := s.spansByName[name]
	if !ok {
		t.Fatalf("span named %q not found in spans: %v", name, s.spanNames())
	}

	if !fn(span) {
		t.Fatalf(msg, args...)
	}
}

// Reset the internal state of the server between test runs.
func (s *MockDatadogServer) Reset() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.spansByID = make(map[uint64]Span)
	s.spansByName = make(map[string]Span)
}
