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

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
	"github.com/rozsapeter96/impala-operator/internal/impala"
)

const (
	slotReason    = "Not enough admission control slots available on host x. Needed 1 slots but 1/1 are already in use."
	poolCapReason = "number of running queries 1 is at or over limit 1."
	waitingReason = "Waiting for executors to start. Only DDL queries and queries scheduled only on the coordinator can currently run."
)

func autoscaledCluster() *impalav1alpha1.ImpalaCluster {
	return &impalav1alpha1.ImpalaCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "as", Namespace: "default", Generation: 1},
		Spec: impalav1alpha1.ImpalaClusterSpec{
			ClusterConfig: impalav1alpha1.ClusterConfig{HiveMetastore: &impalav1alpha1.HiveMetastoreSpec{URIs: "thrift://hms:9083"}},
			ExecutorGroups: []impalav1alpha1.ExecutorGroupSpec{{
				Name:   "small",
				Size:   1,
				Groups: new(int32(1)),
				Autoscaling: &impalav1alpha1.AutoscalingSpec{
					Enabled:               true,
					MinGroups:             new(int32(1)),
					MaxGroups:             new(int32(2)),
					ScaleUpDelaySeconds:   new(int32(30)),
					ScaleDownDelaySeconds: new(int32(60)),
					CooldownSeconds:       new(int32(10)),
				},
			}},
		},
	}
}

// fakeAdmission builds a one-coordinator snapshot for the default pool.
type fakeAdmission struct {
	queued        int64
	reason        string
	executingTop  float64 // queries executing on default-pool-small-1
	executingBase float64 // queries executing on default-pool-small-0
}

func (f *fakeAdmission) source(ctx context.Context, c client.Client, cluster *impalav1alpha1.ImpalaCluster) ([]Snapshot, error) {
	return []Snapshot{{
		Pools: map[string]impala.AdmissionPool{
			"default-pool": {Name: "default-pool", NumQueued: f.queued, NumRunning: 1, MaxRequests: -1, HeadQueuedReason: f.reason},
		},
		Metrics: impala.Metrics{
			impala.MetricGroupExecuting + "default-pool-small-0": f.executingBase,
			impala.MetricGroupExecuting + "default-pool-small-1": f.executingTop,
		},
	}}, nil
}

func newFakeClient(t *testing.T, cluster *impalav1alpha1.ImpalaCluster, funcs ...interceptor.Funcs) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := impalav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).WithStatusSubresource(cluster)
	for _, f := range funcs {
		b = b.WithInterceptorFuncs(f)
	}
	return b.Build()
}

// markGroupHealthy records, as the main reconciler would, that the named
// executor group instance has reached minHealthySize.
func markGroupHealthy(t *testing.T, c client.Client, key types.NamespacedName, group string, healthy bool) {
	t.Helper()
	ctx := context.Background()
	live := &impalav1alpha1.ImpalaCluster{}
	if err := c.Get(ctx, key, live); err != nil {
		t.Fatal(err)
	}
	st := &live.Status.ExecutorGroups[0]
	for i := range st.Groups {
		if st.Groups[i].Name == group {
			st.Groups[i].Healthy = healthy
			if err := c.Status().Update(ctx, live); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	st.Groups = append(st.Groups, impalav1alpha1.ExecutorGroupInstanceStatus{Name: group, Replicas: 1, ReadyReplicas: 1, Healthy: healthy})
	if err := c.Status().Update(ctx, live); err != nil {
		t.Fatal(err)
	}
}

func TestAutoscalerScalesOnSlotSaturation(t *testing.T) {
	cluster := autoscaledCluster()
	c := newFakeClient(t, cluster)

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	adm := &fakeAdmission{queued: 1, reason: slotReason, executingBase: 1}
	a := &ExecutorGroupAutoscaler{Client: c, Metrics: adm.source, Now: func() time.Time { return now }}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
	ctx := context.Background()

	desired := func() (int32, *metav1.Time) {
		live := &impalav1alpha1.ImpalaCluster{}
		if err := c.Get(ctx, req.NamespacedName, live); err != nil {
			t.Fatal(err)
		}
		if len(live.Status.ExecutorGroups) == 0 {
			return -1, nil
		}
		return live.Status.ExecutorGroups[0].DesiredGroups, live.Status.ExecutorGroups[0].LastScaleTime
	}

	// Queued for a scalable reason, but not for long enough yet.
	if _, err := a.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if d, ts := desired(); d != 1 || ts != nil {
		t.Fatalf("expected no scale yet, got desired=%d lastScale=%v", d, ts)
	}

	// 30s later still queued on slots: scale up.
	now = now.Add(30 * time.Second)
	if _, err := a.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if d, ts := desired(); d != 2 || ts == nil {
		t.Fatalf("expected scale up to 2, got desired=%d lastScale=%v", d, ts)
	}

	// Queue drains and the new group becomes healthy but stays idle. The
	// idle timer starts after cooldown.
	adm.queued = 0
	adm.reason = ""
	markGroupHealthy(t, c, req.NamespacedName, "default-pool-small-1", true)
	now = now.Add(11 * time.Second)
	if _, err := a.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if d, _ := desired(); d != 2 {
		t.Fatalf("expected 2 while idle timer runs, got %d", d)
	}
	now = now.Add(61 * time.Second)
	if _, err := a.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if d, _ := desired(); d != 1 {
		t.Fatalf("expected scale down to 1, got %d", d)
	}
}

// A group that has been added but has not become healthy yet reports no
// executing queries. That must not count as idle time, otherwise the group
// is removed before it can serve the queue that triggered it.
func TestAutoscalerDoesNotScaleDownBootingGroup(t *testing.T) {
	cluster := autoscaledCluster()
	c := newFakeClient(t, cluster)

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	adm := &fakeAdmission{queued: 1, reason: slotReason, executingBase: 1}
	a := &ExecutorGroupAutoscaler{Client: c, Metrics: adm.source, Now: func() time.Time { return now }}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
	ctx := context.Background()

	for range 2 {
		if _, err := a.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		now = now.Add(30 * time.Second)
	}
	desired := func() int32 {
		live := &impalav1alpha1.ImpalaCluster{}
		if err := c.Get(ctx, req.NamespacedName, live); err != nil {
			t.Fatal(err)
		}
		return live.Status.ExecutorGroups[0].DesiredGroups
	}
	if d := desired(); d != 2 {
		t.Fatalf("expected scale up to 2, got %d", d)
	}

	// Queue drained, but the new group is still booting (never reported
	// healthy). Well past cooldown + scaleDownDelay it must still be there.
	adm.queued, adm.reason = 0, ""
	markGroupHealthy(t, c, req.NamespacedName, "default-pool-small-1", false)
	for range 6 {
		now = now.Add(30 * time.Second)
		if _, err := a.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if d := desired(); d != 2 {
		t.Fatalf("booting group must not be scaled down, got %d", d)
	}

	// Once healthy and still idle, the idle timer starts and it is removed.
	markGroupHealthy(t, c, req.NamespacedName, "default-pool-small-1", true)
	for range 3 {
		now = now.Add(30 * time.Second)
		if _, err := a.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if d := desired(); d != 1 {
		t.Fatalf("healthy idle group should be scaled down, got %d", d)
	}
}

// A status patch that loses the optimistic-lock race must not discard the
// decision: the round requeues promptly and the timers are left intact, so
// the next round scales immediately instead of waiting scaleUpDelay again.
func TestAutoscalerRetriesOnStatusConflict(t *testing.T) {
	cluster := autoscaledCluster()
	conflicts := 0
	c := newFakeClient(t, cluster, interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if conflicts == 0 {
				conflicts++
				return apierrors.NewConflict(schema.GroupResource{Group: "impala.operator.dev", Resource: "impalaclusters"}, obj.GetName(), fmt.Errorf("the object has been modified"))
			}
			return cl.Status().Patch(ctx, obj, patch, opts...)
		},
	})

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	adm := &fakeAdmission{queued: 1, reason: slotReason, executingBase: 1}
	a := &ExecutorGroupAutoscaler{Client: c, Metrics: adm.source, Now: func() time.Time { return now }}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
	ctx := context.Background()

	// First round only records desiredGroups=1 (no LastScaleTime yet); the
	// interceptor makes that patch conflict.
	res, err := a.Reconcile(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != time.Second {
		t.Fatalf("conflict must requeue promptly, got %v", res.RequeueAfter)
	}
	// 30s later the queue has persisted for the full delay: the retried
	// round must scale up right away because queuedSince survived the conflict.
	now = now.Add(30 * time.Second)
	if _, err := a.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	live := &impalav1alpha1.ImpalaCluster{}
	if err := c.Get(ctx, req.NamespacedName, live); err != nil {
		t.Fatal(err)
	}
	if len(live.Status.ExecutorGroups) == 0 || live.Status.ExecutorGroups[0].DesiredGroups != 2 || live.Status.ExecutorGroups[0].LastScaleTime == nil {
		t.Fatalf("expected scale up to 2 after conflict retry, got %+v", live.Status.ExecutorGroups)
	}
	if conflicts != 1 {
		t.Fatalf("expected exactly one injected conflict, got %d", conflicts)
	}
}

// The key regression: a queue caused by the pool running-query cap must NOT
// trigger a scale-up, because adding a group cannot drain it.
func TestAutoscalerIgnoresPoolCapQueue(t *testing.T) {
	cluster := autoscaledCluster()
	c := newFakeClient(t, cluster)

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	adm := &fakeAdmission{queued: 5, reason: poolCapReason, executingBase: 1}
	a := &ExecutorGroupAutoscaler{Client: c, Metrics: adm.source, Now: func() time.Time { return now }}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
	ctx := context.Background()

	// Even well past the scale-up delay, a pool-cap queue must not scale up.
	for range 4 {
		if _, err := a.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		now = now.Add(30 * time.Second)
	}
	live := &impalav1alpha1.ImpalaCluster{}
	if err := c.Get(ctx, req.NamespacedName, live); err != nil {
		t.Fatal(err)
	}
	if len(live.Status.ExecutorGroups) > 0 && live.Status.ExecutorGroups[0].DesiredGroups > 1 {
		t.Fatalf("must not scale up on a pool-cap queue, got %d", live.Status.ExecutorGroups[0].DesiredGroups)
	}
}

func TestAutoscalerIgnoresClustersWithoutAutoscaling(t *testing.T) {
	cluster := autoscaledCluster()
	cluster.Spec.ExecutorGroups[0].Autoscaling = nil
	c := newFakeClient(t, cluster)
	a := &ExecutorGroupAutoscaler{Client: c, Metrics: (&fakeAdmission{queued: 5, reason: slotReason}).source}
	res, err := a.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("must not poll clusters without autoscaling")
	}
}

func TestReasonClassification(t *testing.T) {
	capacity := []string{
		slotReason,
		"Not enough memory available on host h. Needed 1 but only 0 out of 2 was available.",
	}
	notCapacity := []string{
		poolCapReason,
		waitingReason,
		"Not enough aggregate memory available in pool root.default with max mem resources 1.",
		"queue full, limit=10, num_queued=10.",
		"queue is not empty (size 3); queued queries are executed first.",
		"",
	}
	for _, r := range capacity {
		if !impala.ReasonIsCapacity(r) {
			t.Errorf("expected capacity reason: %q", r)
		}
	}
	for _, r := range notCapacity {
		if impala.ReasonIsCapacity(r) {
			t.Errorf("expected not a capacity reason: %q", r)
		}
	}
	if !impala.ReasonIsWaitingForExecutors(waitingReason) {
		t.Errorf("expected waiting-for-executors: %q", waitingReason)
	}
	if impala.ReasonIsWaitingForExecutors(slotReason) {
		t.Errorf("slot reason must not be waiting-for-executors")
	}
}

// "Waiting for executors to start" while groups already exist means they are
// still booting; the autoscaler must not pile on more groups.
func TestAutoscalerDoesNotScaleOnBootingGroups(t *testing.T) {
	cluster := autoscaledCluster()
	c := newFakeClient(t, cluster)

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	adm := &fakeAdmission{queued: 2, reason: waitingReason}
	a := &ExecutorGroupAutoscaler{Client: c, Metrics: adm.source, Now: func() time.Time { return now }}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
	ctx := context.Background()

	for range 4 {
		if _, err := a.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		now = now.Add(30 * time.Second)
	}
	live := &impalav1alpha1.ImpalaCluster{}
	if err := c.Get(ctx, req.NamespacedName, live); err != nil {
		t.Fatal(err)
	}
	if len(live.Status.ExecutorGroups) > 0 && live.Status.ExecutorGroups[0].DesiredGroups > 1 {
		t.Fatalf("must not add groups while existing ones boot, got %d", live.Status.ExecutorGroups[0].DesiredGroups)
	}
}

// With no groups at all (minGroups 0), "Waiting for executors to start" is
// the cold-start signal and must scale up.
func TestAutoscalerColdStartsOnWaitingForExecutors(t *testing.T) {
	cluster := autoscaledCluster()
	cluster.Spec.ExecutorGroups[0].Groups = new(int32(0))
	cluster.Spec.ExecutorGroups[0].Autoscaling.MinGroups = new(int32(0))
	c := newFakeClient(t, cluster)

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	adm := &fakeAdmission{queued: 1, reason: waitingReason}
	a := &ExecutorGroupAutoscaler{Client: c, Metrics: adm.source, Now: func() time.Time { return now }}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)}
	ctx := context.Background()

	if _, err := a.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	if _, err := a.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	live := &impalav1alpha1.ImpalaCluster{}
	if err := c.Get(ctx, req.NamespacedName, live); err != nil {
		t.Fatal(err)
	}
	if len(live.Status.ExecutorGroups) == 0 || live.Status.ExecutorGroups[0].DesiredGroups != 1 {
		t.Fatalf("expected cold start to 1 group, got %+v", live.Status.ExecutorGroups)
	}
}
