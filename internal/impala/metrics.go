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

// Package impala talks to Impala daemons over their debug web server.
package impala

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Metric names emitted by coordinators that the autoscaler consumes.
const (
	// MetricGroupExecuting is the number of queries this coordinator is running
	// on an executor group (suffix "<group>"). Used for the scale-down signal.
	MetricGroupExecuting = "admission-controller.executor-group.num-queries-executing."
	// MetricBackendsTotal is the number of backends known to the cluster membership.
	MetricBackendsTotal = "cluster-membership.backends.total"
)

// Impala's queued-reason strings (be/src/scheduling/admission-controller.cc)
// fall into three classes for autoscaling purposes:
//
//   - capacity: the existing executor groups are full, so another group adds
//     concurrency. Scaling up relieves these.
//   - waiting for executors: no executor group has registered yet. Scaling up
//     only helps when the family currently has no groups at all; if groups
//     exist they are merely still starting, and adding more does not speed
//     that up.
//   - everything else (pool running-query cap, pool aggregate memory, queue
//     full, FIFO fairness): pool-wide limits that more groups cannot raise.
var capacityQueueReasons = []string{
	"Not enough admission control slots",  // group slots saturated
	"Not enough memory available on host", // per-host memory exhausted
}

const waitingForExecutorsReason = "Waiting for executors to start"

// ReasonIsCapacity reports whether the queue reason means the executor groups
// are at capacity, so that adding a group would admit the query.
func ReasonIsCapacity(reason string) bool {
	for _, p := range capacityQueueReasons {
		if strings.Contains(reason, p) {
			return true
		}
	}
	return false
}

// ReasonIsWaitingForExecutors reports whether the queue reason means no
// executor group has registered with the coordinator yet.
func ReasonIsWaitingForExecutors(reason string) bool {
	return strings.Contains(reason, waitingForExecutorsReason)
}

// Metrics is a flat view of a daemon's metrics: name -> value.
type Metrics map[string]any

// metricsPage is the JSON rendering of the /metrics debug page.
type metricsPage struct {
	MetricGroup metricGroup `json:"metric_group"`
}

type metricGroup struct {
	Name        string        `json:"name"`
	Metrics     []metricEntry `json:"metrics"`
	ChildGroups []metricGroup `json:"child_groups"`
}

type metricEntry struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func (g *metricGroup) flatten(into Metrics) {
	for _, m := range g.Metrics {
		into[m.Name] = m.Value
	}
	for i := range g.ChildGroups {
		g.ChildGroups[i].flatten(into)
	}
}

// Number returns a numeric metric, or false when it is absent or not numeric.
func (m Metrics) Number(name string) (float64, bool) {
	v, ok := m[name]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// AdmissionPool is one resource pool's state from the /admission page.
type AdmissionPool struct {
	Name string
	// NumRunning and NumQueued are the pool-wide aggregate counts.
	NumRunning int64
	NumQueued  int64
	// MaxRequests is the pool's max-running-queries cap; <= 0 means unlimited.
	MaxRequests int64
	// HeadQueuedReason is the reason the query at the head of the queue is
	// waiting. It is populated only while NumQueued > 0.
	HeadQueuedReason string
}

// admissionPage is the JSON rendering of the /admission debug page.
type admissionPage struct {
	ResourcePools []struct {
		PoolName         string `json:"pool_name"`
		AggNumRunning    int64  `json:"agg_num_running"`
		AggNumQueued     int64  `json:"agg_num_queued"`
		PoolMaxRequests  int64  `json:"pool_max_requests"`
		HeadQueuedReason string `json:"head_queued_reason"`
	} `json:"resource_pools"`
}

// Client scrapes daemon web servers.
type Client struct {
	HTTP *http.Client
}

const scrapeTimeout = 5 * time.Second

// NewClient returns a plain-HTTP client with a short timeout suitable for polling.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: scrapeTimeout}}
}

// NewTLSClient returns a client that verifies daemon web servers against the
// given PEM CA bundle. Impala serves every web server, including the
// metrics-only one, over TLS as soon as -webserver_certificate_file is set,
// so the autoscaler must speak HTTPS to a secured cluster.
func NewTLSClient(caPEM []byte) (*Client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no CA certificates found in PEM bundle")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &Client{HTTP: &http.Client{Timeout: scrapeTimeout, Transport: transport}}, nil
}

func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

// Metrics fetches the metrics page as JSON from base (e.g.
// "http://10.0.0.5:25000") and flattens the metric group tree.
func (c *Client) Metrics(ctx context.Context, base string) (Metrics, error) {
	var page metricsPage
	if err := c.getJSON(ctx, base+"/metrics?json", &page); err != nil {
		return nil, err
	}
	m := Metrics{}
	page.MetricGroup.flatten(m)
	return m, nil
}

// Admission fetches the admission page as JSON and returns its pools keyed by
// fully qualified pool name (e.g. "root.default").
func (c *Client) Admission(ctx context.Context, base string) (map[string]AdmissionPool, error) {
	var page admissionPage
	if err := c.getJSON(ctx, base+"/admission?json", &page); err != nil {
		return nil, err
	}
	pools := make(map[string]AdmissionPool, len(page.ResourcePools))
	for _, p := range page.ResourcePools {
		pools[p.PoolName] = AdmissionPool{
			Name:             p.PoolName,
			NumRunning:       p.AggNumRunning,
			NumQueued:        p.AggNumQueued,
			MaxRequests:      p.PoolMaxRequests,
			HeadQueuedReason: p.HeadQueuedReason,
		}
	}
	return pools, nil
}
