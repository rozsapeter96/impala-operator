/*
Copyright 2026 Peter Rozsa.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package impala

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const metricsBody = `{"__common__": {"process-name": "impalad"}, "metric_group": {"name": "impala-metrics",
  "metrics": [{"name": "some-string", "value": "x"}],
  "child_groups": [{"name": "admission-controller", "metrics": [
    {"name": "admission-controller.executor-group.num-queries-executing.root.default-small-0", "value": 2, "kind": "GAUGE"}]}]}}`

const admissionBody = `{"resource_pools": [
  {"pool_name": "root.default", "agg_num_running": 1, "agg_num_queued": 2, "pool_max_requests": 1,
   "head_queued_reason": "Not enough admission control slots available on host h. Needed 1 slots but 1/1 are already in use."}]}`

func testServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/metrics" && r.URL.RawQuery == "json":
			_, _ = w.Write([]byte(metricsBody))
		case r.URL.Path == "/admission" && r.URL.RawQuery == "json":
			_, _ = w.Write([]byte(admissionBody))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestMetrics(t *testing.T) {
	srv := testServer()
	defer srv.Close()

	m, err := NewClient().Metrics(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := m.Number(MetricGroupExecuting + "root.default-small-0"); !ok || v != 2 {
		t.Errorf("executing = %v %v", v, ok)
	}
	if _, ok := m.Number("some-string"); ok {
		t.Errorf("string must not be numeric")
	}
	if _, ok := m.Number("missing"); ok {
		t.Errorf("missing must not be found")
	}
}

func TestAdmission(t *testing.T) {
	srv := testServer()
	defer srv.Close()

	pools, err := NewClient().Admission(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := pools["root.default"]
	if !ok {
		t.Fatalf("root.default pool missing: %v", pools)
	}
	if p.NumQueued != 2 || p.NumRunning != 1 || p.MaxRequests != 1 {
		t.Errorf("unexpected pool counts: %+v", p)
	}
	if !ReasonIsCapacity(p.HeadQueuedReason) {
		t.Errorf("slot reason should be a capacity reason: %q", p.HeadQueuedReason)
	}
}

func TestAdmissionBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if _, err := NewClient().Admission(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on non-200")
	}
}
