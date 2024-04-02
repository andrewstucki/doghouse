package doghouse

import (
	"os"
	"slices"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var server *MockDatadogServer

func TestMain(m *testing.M) {
	server = New()
	ret := m.Run()
	server.Close()
	os.Exit(ret)
}

func TestExpectSpanFn(t *testing.T) {
	t.Parallel()

	span := tracer.StartSpan("test.expectspanfn", tracer.ResourceName("resource"))
	span.Finish()

	tracer.Flush()

	server.WaitForSpan(t, "test.expectspanfn")
	server.ExpectSpanFn(t, "test.expectspanfn", func(span Span) bool {
		return span.Resource == "resource"
	}, "invalid span")
}

func TestExpectSpan(t *testing.T) {
	t.Parallel()

	span := tracer.StartSpan("test.expectspan")
	child := tracer.StartSpan("test.expectspan.child", tracer.ChildOf(span.Context()))

	server.ExpectNoSpan(t, "test.expectspan")
	server.ExpectNoSpan(t, "test.expectspan.child")

	child.Finish()
	span.Finish()
	tracer.Flush()

	server.WaitForSpan(t, "test.expectspan.child", "test.expectspan")
	server.ExpectSpan(t, "test.expectspan")
	server.ExpectSpan(t, "test.expectspan.child", "test.expectspan")
}

func TestReset(t *testing.T) {
	span := tracer.StartSpan("test.reset")
	span.Finish()
	tracer.Flush()

	server.WaitForSpan(t, "test.reset")

	server.Reset()

	server.ExpectNoSpan(t, "test.reset")
}

func TestSpanNames(t *testing.T) {
	one := tracer.StartSpan("1")
	two := tracer.StartSpan("2", tracer.ChildOf(one.Context()))
	three := tracer.StartSpan("3", tracer.ChildOf(two.Context()))
	zero := tracer.StartSpan("0", tracer.ChildOf(three.Context()))

	zero.Finish()
	three.Finish()
	two.Finish()
	one.Finish()
	tracer.Flush()

	server.WaitForSpan(t, "1")
	if !slices.Equal(server.spanNames(), []string{"0", "1", "2", "3"}) {
		t.Fatalf("expected span names didn't match: %+v", server.spanNames())
	}
}
