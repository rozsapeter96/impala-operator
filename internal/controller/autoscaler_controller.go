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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/recorder"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
	"github.com/rozsapeter96/impala-operator/internal/impala"
	"github.com/rozsapeter96/impala-operator/internal/resources"
)

// DefaultPollInterval is how often coordinator state is sampled.
const DefaultPollInterval = 15 * time.Second

// Snapshot is one coordinator's admission and metric state at a point in time.
type Snapshot struct {
	// Pools is the /admission state keyed by fully qualified pool name.
	Pools map[string]impala.AdmissionPool
	// Metrics is the flattened /metrics page, used for the per-group idle signal.
	Metrics impala.Metrics
}

// MetricsSource returns one Snapshot per ready coordinator. It is a function so
// tests can substitute a fake; the default scrapes ready coordinator pods.
type MetricsSource func(ctx context.Context, c client.Client, cluster *impalav1alpha1.ImpalaCluster) ([]Snapshot, error)

// ExecutorGroupAutoscaler adds and removes executor group instances based on
// admission-control state observed on the coordinators. It scales a family up
// only when queries are queued for a reason that adding an executor group can
// relieve (slot saturation, per-host memory, or no healthy group), read from
// the coordinator's /admission page as head_queued_reason. Pool-level caps
// (max-running-queries, aggregate pool memory) queue queries too, but more
// groups do not help there, so those reasons are ignored.
//
// Decisions are written to status.executorGroups[].desiredGroups; the main
// reconciler materialises them.
type ExecutorGroupAutoscaler struct {
	client.Client
	Recorder     recorder.EventRecorder
	Metrics      MetricsSource
	PollInterval time.Duration
	Now          func() time.Time

	mu    sync.Mutex
	state map[types.NamespacedName]map[string]*familyState
}

// familyState is the autoscaler's memory for one executor group family.
type familyState struct {
	queuedSince time.Time // zero when nothing is queued for a scalable reason
	idleSince   time.Time // zero when the highest group is busy
}

// Reconcile samples coordinator state for one cluster and updates desiredGroups.
func (a *ExecutorGroupAutoscaler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	cluster := &impalav1alpha1.ImpalaCluster{}
	if err := a.Get(ctx, req.NamespacedName, cluster); err != nil {
		a.forget(req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !cluster.DeletionTimestamp.IsZero() || !hasAutoscaling(cluster) {
		a.forget(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	poll := a.PollInterval
	if poll == 0 {
		poll = DefaultPollInterval
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}

	snapshots, err := a.Metrics(ctx, a.Client, cluster)
	if err != nil {
		log.V(1).Info("admission state unavailable, skipping autoscaling round", "error", err.Error())
		return ctrl.Result{RequeueAfter: poll}, nil
	}
	if len(snapshots) == 0 {
		return ctrl.Result{RequeueAfter: poll}, nil
	}

	original := cluster.DeepCopy()
	changed := false
	var scaled []scaleDecision
	for i := range cluster.Spec.ExecutorGroups {
		eg := &cluster.Spec.ExecutorGroups[i]
		if eg.Autoscaling == nil || !eg.Autoscaling.Enabled {
			continue
		}
		st := a.familyState(client.ObjectKeyFromObject(cluster), eg.Name)
		c, d := a.decide(ctx, cluster, eg, st, snapshots, now)
		if c {
			changed = true
		}
		if d != nil {
			scaled = append(scaled, *d)
		}
	}
	if !changed {
		return ctrl.Result{RequeueAfter: poll}, nil
	}
	// The main reconciler rewrites status.executorGroups on every pass, and a
	// merge patch replaces the whole list, so both writers use optimistic
	// locking: whoever loses re-reads and tries again. Timer resets and
	// events happen only once the decision is durably recorded, so a lost
	// race costs one poll interval rather than a full scale-up delay.
	if err := a.Status().Patch(ctx, cluster, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			log.V(1).Info("status changed concurrently, retrying autoscaling round")
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	for _, d := range scaled {
		d.state.queuedSince, d.state.idleSince = time.Time{}, time.Time{}
		log.Info("scaled executor group family", "executorGroup", d.family, "from", d.from, "to", d.to, "reason", d.reason)
		eventReason := "ScaledUp"
		if d.to < d.from {
			eventReason = "ScaledDown"
		}
		if a.Recorder != nil {
			a.Recorder.Eventf(cluster, nil, corev1.EventTypeNormal, eventReason, "Autoscale",
				"executor group %s: %d -> %d groups (%s)", d.family, d.from, d.to, d.reason)
		}
	}
	return ctrl.Result{RequeueAfter: poll}, nil
}

// scaleDecision records a scaling action whose side effects (timer reset,
// event) are applied after the status patch succeeds.
type scaleDecision struct {
	state    *familyState
	family   string
	from, to int32
	reason   string
}

// decide evaluates one family and mutates its status entry. It returns
// whether desiredGroups changed and, when it did because of a scaling
// action, the decision to finalise after the patch.
func (a *ExecutorGroupAutoscaler) decide(ctx context.Context, cluster *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec, st *familyState, snapshots []Snapshot, now time.Time) (bool, *scaleDecision) {
	log := logf.FromContext(ctx).WithValues("executorGroup", eg.Name)
	as := eg.Autoscaling
	status := statusFor(cluster, eg.Name)

	current := status.DesiredGroups
	if status.LastScaleTime == nil {
		// Never scaled: start from the spec value.
		current = resources.SpecGroups(eg)
	}
	current = clampGroups(eg, current)

	pool := resources.PoolName(cluster, eg)
	queuedReason, queuedForScale := scalableQueue(snapshots, pool, current)
	topGroup := resources.ImpalaExecutorGroupName(cluster, eg, current-1)
	executing := sumExecuting(snapshots, impala.MetricGroupExecuting+topGroup)

	if queuedForScale {
		if st.queuedSince.IsZero() {
			st.queuedSince = now
		}
	} else {
		st.queuedSince = time.Time{}
	}
	// A group that has not become healthy yet reports no executing queries,
	// which must not count as idle time: a freshly added group would
	// otherwise be removed before it ever serves a query.
	if current > 0 && executing == 0 && groupHealthy(status, topGroup) {
		if st.idleSince.IsZero() {
			st.idleSince = now
		}
	} else {
		st.idleSince = time.Time{}
	}

	inCooldown := status.LastScaleTime != nil && now.Sub(status.LastScaleTime.Time) < seconds(as.CooldownSeconds, 120)
	desired := current
	reason := ""
	switch {
	case inCooldown:
	case !st.queuedSince.IsZero() && now.Sub(st.queuedSince) >= seconds(as.ScaleUpDelaySeconds, 30) && current < deref(as.MaxGroups, 5):
		desired = current + 1
		reason = fmt.Sprintf("queued in pool %s for %s: %q", pool, now.Sub(st.queuedSince).Round(time.Second), queuedReason)
	case !st.idleSince.IsZero() && now.Sub(st.idleSince) >= seconds(as.ScaleDownDelaySeconds, 300) && current > deref(as.MinGroups, 1):
		desired = current - 1
		reason = fmt.Sprintf("executor group %s idle for %s", topGroup, now.Sub(st.idleSince).Round(time.Second))
	}

	if desired == current {
		if status.LastScaleTime == nil && status.DesiredGroups != current {
			status.DesiredGroups = current
			return true, nil
		}
		return false, nil
	}

	log.V(1).Info("scaling executor group family", "from", current, "to", desired, "reason", reason)
	status.DesiredGroups = desired
	status.LastScaleTime = &metav1.Time{Time: now}
	return true, &scaleDecision{state: st, family: eg.Name, from: current, to: desired, reason: reason}
}

// groupHealthy reports whether the main reconciler has observed the named
// executor group instance with at least minHealthySize ready executors.
func groupHealthy(status *impalav1alpha1.ExecutorGroupStatus, group string) bool {
	for i := range status.Groups {
		if status.Groups[i].Name == group {
			return status.Groups[i].Healthy
		}
	}
	return false
}

// scalableQueue reports whether any coordinator shows the given pool with a
// queued query whose head-of-queue reason adding an executor group would
// relieve, and returns that reason for logging. Capacity reasons (slots,
// per-host memory) always qualify. "Waiting for executors to start" qualifies
// only when the family has no groups at all; when groups exist they are just
// still booting, and adding more would not admit the query any sooner.
func scalableQueue(snapshots []Snapshot, pool string, currentGroups int32) (reason string, ok bool) {
	for _, s := range snapshots {
		p, present := s.Pools[pool]
		if !present || p.NumQueued <= 0 {
			continue
		}
		r := p.HeadQueuedReason
		if impala.ReasonIsCapacity(r) || (impala.ReasonIsWaitingForExecutors(r) && currentGroups == 0) {
			return r, true
		}
	}
	return "", false
}

// sumExecuting sums a per-group executing metric across coordinators.
func sumExecuting(snapshots []Snapshot, name string) float64 {
	var sum float64
	for _, s := range snapshots {
		if v, ok := s.Metrics.Number(name); ok {
			sum += v
		}
	}
	return sum
}

// statusFor returns a pointer to the family's status entry, creating it if needed.
func statusFor(cluster *impalav1alpha1.ImpalaCluster, name string) *impalav1alpha1.ExecutorGroupStatus {
	for i := range cluster.Status.ExecutorGroups {
		if cluster.Status.ExecutorGroups[i].Name == name {
			return &cluster.Status.ExecutorGroups[i]
		}
	}
	cluster.Status.ExecutorGroups = append(cluster.Status.ExecutorGroups, impalav1alpha1.ExecutorGroupStatus{Name: name})
	return &cluster.Status.ExecutorGroups[len(cluster.Status.ExecutorGroups)-1]
}

func (a *ExecutorGroupAutoscaler) familyState(key types.NamespacedName, family string) *familyState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == nil {
		a.state = map[types.NamespacedName]map[string]*familyState{}
	}
	if a.state[key] == nil {
		a.state[key] = map[string]*familyState{}
	}
	if a.state[key][family] == nil {
		a.state[key][family] = &familyState{}
	}
	return a.state[key][family]
}

func (a *ExecutorGroupAutoscaler) forget(key types.NamespacedName) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.state, key)
}

func hasAutoscaling(cluster *impalav1alpha1.ImpalaCluster) bool {
	for i := range cluster.Spec.ExecutorGroups {
		if as := cluster.Spec.ExecutorGroups[i].Autoscaling; as != nil && as.Enabled {
			return true
		}
	}
	return false
}

func seconds(v *int32, def int32) time.Duration {
	return time.Duration(deref(v, def)) * time.Second
}

func deref(v *int32, def int32) int32 {
	if v != nil {
		return *v
	}
	return def
}

// ScrapeCoordinators fetches admission and metric state from every ready
// coordinator pod. With TLS enabled every Impala web server (including the
// metrics-only one) serves HTTPS, so the scrape verifies the daemon's
// certificate against the CA in the referenced TLS Secret and addresses the
// pod by its DNS name, which the certificate must cover.
func ScrapeCoordinators(ctx context.Context, c client.Client, cluster *impalav1alpha1.ImpalaCluster) ([]Snapshot, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods, client.InNamespace(cluster.Namespace),
		client.MatchingLabels(resources.SelectorLabels(cluster, resources.ComponentCoordinator))); err != nil {
		return nil, err
	}
	scraper := impala.NewClient()
	scheme := "http"
	if resources.TLSEnabled(cluster) {
		var err error
		if scraper, err = tlsScraper(ctx, c, cluster); err != nil {
			return nil, err
		}
		scheme = "https"
	}
	headless := resources.HeadlessName(resources.CoordinatorName(cluster))
	var out []Snapshot
	var lastErr error
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.PodIP == "" || !podReady(pod) {
			continue
		}
		host := pod.Status.PodIP
		if scheme == "https" {
			host = resources.PodFQDN(pod.Name, headless, cluster.Namespace)
		}
		base := fmt.Sprintf("%s://%s:%d", scheme, host, resources.PortImpaladWeb)
		pools, err := scraper.Admission(ctx, base)
		if err != nil {
			lastErr = err
			continue
		}
		metrics, err := scraper.Metrics(ctx, base)
		if err != nil {
			lastErr = err
			continue
		}
		out = append(out, Snapshot{Pools: pools, Metrics: metrics})
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

// tlsScraper builds a scrape client trusting the CA of the cluster's TLS
// Secret ("ca.crt", falling back to "tls.crt" for self-signed certificates).
func tlsScraper(ctx context.Context, c client.Client, cluster *impalav1alpha1.ImpalaCluster) (*impala.Client, error) {
	name := cluster.Spec.ClusterConfig.Security.TLS.CertSecretRef.Name
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: name}, secret); err != nil {
		return nil, fmt.Errorf("read TLS Secret %q: %w", name, err)
	}
	ca := secret.Data[resources.TLSSecretCAKey]
	if len(ca) == 0 {
		ca = secret.Data[corev1.TLSCertKey]
	}
	scraper, err := impala.NewTLSClient(ca)
	if err != nil {
		return nil, fmt.Errorf("TLS Secret %q: %w", name, err)
	}
	return scraper, nil
}

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// SetupWithManager registers the autoscaler. It reacts to spec changes and
// otherwise runs on its own polling interval.
func (a *ExecutorGroupAutoscaler) SetupWithManager(mgr ctrl.Manager) error {
	if a.Metrics == nil {
		a.Metrics = ScrapeCoordinators
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&impalav1alpha1.ImpalaCluster{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("impala-autoscaler").
		Complete(a)
}
