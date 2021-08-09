package metrics

import (
	"testing"

	"github.com/m-lab/go/prometheusx/promtest"
)

func TestMetrics(t *testing.T) {
	Error.WithLabelValues("x", "x").Inc()
	Machine.WithLabelValues("x", "x", "x").Inc()
	Site.WithLabelValues("x").Inc()
	// TODO: Pass in t once all metrics pass the linter.
	promtest.LintMetrics(nil)
}
