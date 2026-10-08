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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/recorder"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
	"github.com/rozsapeter96/impala-operator/internal/resources"
)

const (
	indexSecretRefs    = ".spec.clusterConfig.secretRefs"
	indexConfigMapRefs = ".spec.clusterConfig.configMapRefs"
	progressingRequeue = 15 * time.Second
)

// ImpalaClusterReconciler reconciles a ImpalaCluster object
type ImpalaClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder recorder.EventRecorder
	// OperatorNamespace is the namespace the manager runs in. Generated
	// NetworkPolicies admit it to the coordinator web port so the
	// autoscaler can scrape. Empty means unknown, which makes enabling
	// networkPolicy a reconcile error rather than a silently broken autoscaler.
	OperatorNamespace string
}

// +kubebuilder:rbac:groups=impala.operator.dev,resources=impalaclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=impala.operator.dev,resources=impalaclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=impala.operator.dev,resources=impalaclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives the cluster towards the desired state described by the
// ImpalaCluster spec: it renders every managed object, applies them tier by
// tier (statestore, catalog, coordinators, executor groups), removes executor
// groups that no longer exist, and reports status.
func (r *ImpalaClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	cluster := &impalav1alpha1.ImpalaCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !cluster.DeletionTimestamp.IsZero() {
		// Owned objects are garbage collected through owner references.
		return ctrl.Result{}, nil
	}

	original := cluster.DeepCopy()
	result, reconcileErr := r.reconcile(ctx, cluster)

	cluster.Status.ObservedGeneration = cluster.Generation
	if reconcileErr != nil {
		setCondition(cluster, impalav1alpha1.ConditionDegraded, metav1.ConditionTrue, "ReconcileError", reconcileErr.Error())
		setCondition(cluster, impalav1alpha1.ConditionReady, metav1.ConditionFalse, "ReconcileError", "reconciliation failed")
	} else {
		setCondition(cluster, impalav1alpha1.ConditionDegraded, metav1.ConditionFalse, "Reconciled", "all objects applied")
	}
	// The autoscaler writes status.executorGroups[].desiredGroups concurrently.
	// A merge patch replaces that list wholesale, so use optimistic locking:
	// if the object moved underneath us, drop this status update and requeue
	// so the next pass reads the autoscaler's decision instead of clobbering it.
	err := r.Status().Patch(ctx, cluster, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
	switch {
	case err == nil, apierrors.IsNotFound(err):
	case apierrors.IsConflict(err):
		log.V(1).Info("status changed concurrently, requeueing")
		if reconcileErr == nil {
			result = ctrl.Result{RequeueAfter: time.Second}
		}
	default:
		log.Error(err, "failed to patch status")
		if reconcileErr == nil {
			reconcileErr = err
		}
	}
	return result, reconcileErr
}

func (r *ImpalaClusterReconciler) reconcile(ctx context.Context, cluster *impalav1alpha1.ImpalaCluster) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	secretsHash, err := r.referencedDataHash(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if resources.NetworkPolicyEnabled(cluster) && r.OperatorNamespace == "" {
		return ctrl.Result{}, fmt.Errorf("security.networkPolicy is enabled but the operator's namespace is unknown; set POD_NAMESPACE on the manager")
	}
	desired := resources.Build(cluster, desiredGroupCounts(cluster), secretsHash, r.OperatorNamespace)

	if err := applyOwned(ctx, r.Client, cluster, desired.ConfigMap); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileNetworkPolicy(ctx, cluster, desired.NetworkPolicy); err != nil {
		return ctrl.Result{}, err
	}

	progressing := false
	blocked := ""
	if desired.Catalog.StatefulSet == nil {
		// hiveMetastore was removed: the cluster now runs on REST catalogs
		// alone, so a catalogd left over from before must go.
		if err := r.pruneCatalog(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
	}
	for _, tier := range desired.CoreTiers() {
		ready, err := r.applyTier(ctx, cluster, tier)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !ready {
			progressing = true
			if blocked == "" {
				blocked = tier.Name
			}
			// Stop rolling out later tiers until this one is ready so that
			// version and config changes propagate in dependency order.
			break
		}
	}

	if blocked == "" {
		for _, eg := range desired.ExecutorGroups {
			if err := applyOwned(ctx, r.Client, cluster, eg.Headless); err != nil {
				return ctrl.Result{}, err
			}
			for _, inst := range eg.Instances {
				ready, err := r.applyTier(ctx, cluster, inst)
				if err != nil {
					return ctrl.Result{}, err
				}
				if !ready {
					progressing = true
				}
			}
		}
		if err := r.pruneExecutorGroups(ctx, cluster, desired); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.updateStatus(ctx, cluster, desired); err != nil {
		return ctrl.Result{}, err
	}

	if progressing {
		msg := "rolling out changes"
		if blocked != "" {
			msg = fmt.Sprintf("waiting for %s to become ready", blocked)
		}
		setCondition(cluster, impalav1alpha1.ConditionProgressing, metav1.ConditionTrue, "RollingOut", msg)
		setCondition(cluster, impalav1alpha1.ConditionReady, metav1.ConditionFalse, "RollingOut", msg)
		log.V(1).Info("cluster progressing", "reason", msg)
		return ctrl.Result{RequeueAfter: progressingRequeue}, nil
	}

	setCondition(cluster, impalav1alpha1.ConditionProgressing, metav1.ConditionFalse, "Stable", "no rollout in progress")
	if allHealthy(cluster) {
		setCondition(cluster, impalav1alpha1.ConditionReady, metav1.ConditionTrue, "AllComponentsReady", "all components are ready")
	} else {
		setCondition(cluster, impalav1alpha1.ConditionReady, metav1.ConditionFalse, "ExecutorGroupsUnhealthy", "one or more executor groups are below minHealthySize")
	}
	return ctrl.Result{}, nil
}

// pruneCatalog deletes the catalog StatefulSet and Services of a cluster that
// no longer deploys catalogd.
func (r *ImpalaClusterReconciler) pruneCatalog(ctx context.Context, cluster *impalav1alpha1.ImpalaCluster) error {
	name := resources.CatalogName(cluster)
	objs := []client.Object{
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: name}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: name}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: resources.HeadlessName(name)}},
	}
	for _, obj := range objs {
		if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		if !metav1.IsControlledBy(obj, cluster) {
			continue
		}
		if err := r.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}

// reconcileNetworkPolicy applies the cluster's NetworkPolicy, or removes a
// previously generated one when isolation has been switched off.
func (r *ImpalaClusterReconciler) reconcileNetworkPolicy(ctx context.Context, cluster *impalav1alpha1.ImpalaCluster, desired *networkingv1.NetworkPolicy) error {
	if desired != nil {
		return applyOwned(ctx, r.Client, cluster, desired)
	}
	live := &networkingv1.NetworkPolicy{}
	key := types.NamespacedName{Namespace: cluster.Namespace, Name: resources.NetworkPolicyName(cluster)}
	if err := r.Get(ctx, key, live); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(live, cluster) {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, live))
}

// applyTier applies a StatefulSet and its supporting objects and reports
// whether the StatefulSet has finished rolling out.
func (r *ImpalaClusterReconciler) applyTier(ctx context.Context, cluster *impalav1alpha1.ImpalaCluster, tier resources.Tier) (bool, error) {
	for _, obj := range tier.Objects {
		if err := applyOwned(ctx, r.Client, cluster, obj); err != nil {
			return false, err
		}
	}
	if err := applyOwned(ctx, r.Client, cluster, tier.StatefulSet); err != nil {
		return false, err
	}
	return statefulSetReady(tier.StatefulSet), nil
}

// statefulSetReady is true when every replica runs the latest revision and is
// ready. A StatefulSet that has just been created (no revision yet) is
// treated as ready so that initial creation is not serialised tier by tier.
func statefulSetReady(sts *appsv1.StatefulSet) bool {
	st := sts.Status
	if st.CurrentRevision == "" && st.UpdateRevision == "" {
		return true
	}
	want := int32(1)
	if sts.Spec.Replicas != nil {
		want = *sts.Spec.Replicas
	}
	return st.ObservedGeneration == sts.Generation &&
		st.UpdateRevision == st.CurrentRevision &&
		st.UpdatedReplicas == want &&
		st.ReadyReplicas == want
}

// pruneExecutorGroups deletes StatefulSets, PDBs and headless Services of
// executor group instances that are no longer desired.
func (r *ImpalaClusterReconciler) pruneExecutorGroups(ctx context.Context, cluster *impalav1alpha1.ImpalaCluster, desired *resources.Desired) error {
	keepSTS := map[string]bool{}
	keepSvc := map[string]bool{}
	for _, eg := range desired.ExecutorGroups {
		keepSvc[eg.Headless.Name] = true
		for _, inst := range eg.Instances {
			keepSTS[inst.StatefulSet.Name] = true
		}
	}
	sel := client.MatchingLabels(resources.SelectorLabels(cluster, resources.ComponentExecutor))
	ns := client.InNamespace(cluster.Namespace)

	var stsList appsv1.StatefulSetList
	if err := r.List(ctx, &stsList, ns, sel); err != nil {
		return err
	}
	for i := range stsList.Items {
		sts := &stsList.Items[i]
		if keepSTS[sts.Name] || !metav1.IsControlledBy(sts, cluster) {
			continue
		}
		logf.FromContext(ctx).Info("removing executor group", "statefulset", sts.Name)
		if err := r.Delete(ctx, sts); client.IgnoreNotFound(err) != nil {
			return err
		}
		r.Recorder.Eventf(cluster, nil, corev1.EventTypeNormal, "ExecutorGroupRemoved", "Prune", "removed executor group %s", sts.Name)
	}

	var pdbList policyv1.PodDisruptionBudgetList
	if err := r.List(ctx, &pdbList, ns, sel); err != nil {
		return err
	}
	for i := range pdbList.Items {
		pdb := &pdbList.Items[i]
		if keepSTS[pdb.Name] || !metav1.IsControlledBy(pdb, cluster) {
			continue
		}
		if err := r.Delete(ctx, pdb); client.IgnoreNotFound(err) != nil {
			return err
		}
	}

	var svcList corev1.ServiceList
	if err := r.List(ctx, &svcList, ns, sel); err != nil {
		return err
	}
	for i := range svcList.Items {
		svc := &svcList.Items[i]
		if keepSvc[svc.Name] || !metav1.IsControlledBy(svc, cluster) {
			continue
		}
		if err := r.Delete(ctx, svc); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}

// updateStatus fills replica counts, executor group health and endpoints from
// the live StatefulSets.
func (r *ImpalaClusterReconciler) updateStatus(ctx context.Context, cluster *impalav1alpha1.ImpalaCluster, desired *resources.Desired) error {
	var err error
	if cluster.Status.Statestore, err = r.componentStatus(ctx, desired.Statestore.StatefulSet); err != nil {
		return err
	}
	if cluster.Status.Catalog, err = r.componentStatus(ctx, desired.Catalog.StatefulSet); err != nil {
		return err
	}
	if cluster.Status.Coordinators, err = r.componentStatus(ctx, desired.Coordinators.StatefulSet); err != nil {
		return err
	}

	previous := map[string]impalav1alpha1.ExecutorGroupStatus{}
	for _, s := range cluster.Status.ExecutorGroups {
		previous[s.Name] = s
	}
	groups := make([]impalav1alpha1.ExecutorGroupStatus, 0, len(desired.ExecutorGroups))
	for _, eg := range desired.ExecutorGroups {
		st := impalav1alpha1.ExecutorGroupStatus{
			Name:          eg.Spec.Name,
			DesiredGroups: int32(len(eg.Instances)),
			LastScaleTime: previous[eg.Spec.Name].LastScaleTime,
		}
		for idx, inst := range eg.Instances {
			cs, err := r.componentStatus(ctx, inst.StatefulSet)
			if err != nil {
				return err
			}
			st.Groups = append(st.Groups, impalav1alpha1.ExecutorGroupInstanceStatus{
				Name:          resources.ImpalaExecutorGroupName(cluster, eg.Spec, int32(idx)),
				Replicas:      cs.Replicas,
				ReadyReplicas: cs.ReadyReplicas,
				Healthy:       cs.ReadyReplicas >= resources.MinHealthySize(eg.Spec),
			})
		}
		groups = append(groups, st)
	}
	cluster.Status.ExecutorGroups = groups

	host := fmt.Sprintf("%s.%s.svc", resources.CoordinatorName(cluster), cluster.Namespace)
	webHost := fmt.Sprintf("%s.%s.svc", resources.HeadlessName(resources.CoordinatorName(cluster)), cluster.Namespace)
	cluster.Status.Endpoints = impalav1alpha1.EndpointsStatus{
		HiveServer2:     fmt.Sprintf("%s:%d", host, resources.PortHS2),
		HiveServer2HTTP: fmt.Sprintf("%s:%d", host, resources.PortHS2HTTP),
		WebUI:           fmt.Sprintf("%s:%d", webHost, resources.PortImpaladWeb),
	}
	return nil
}

func (r *ImpalaClusterReconciler) componentStatus(ctx context.Context, desired *appsv1.StatefulSet) (impalav1alpha1.ComponentStatus, error) {
	if desired == nil {
		// The component is not deployed (catalogd without a Hive Metastore).
		return impalav1alpha1.ComponentStatus{}, nil
	}
	live := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(desired), live); err != nil {
		if apierrors.IsNotFound(err) {
			return impalav1alpha1.ComponentStatus{}, nil
		}
		return impalav1alpha1.ComponentStatus{}, err
	}
	want := int32(1)
	if live.Spec.Replicas != nil {
		want = *live.Spec.Replicas
	}
	return impalav1alpha1.ComponentStatus{Replicas: want, ReadyReplicas: live.Status.ReadyReplicas}, nil
}

func allHealthy(cluster *impalav1alpha1.ImpalaCluster) bool {
	s := &cluster.Status
	if s.Statestore.ReadyReplicas < s.Statestore.Replicas ||
		s.Catalog.ReadyReplicas < s.Catalog.Replicas ||
		s.Coordinators.ReadyReplicas < s.Coordinators.Replicas {
		return false
	}
	for _, eg := range s.ExecutorGroups {
		for _, g := range eg.Groups {
			if !g.Healthy {
				return false
			}
		}
	}
	return true
}

// desiredGroupCounts returns the autoscaler-chosen instance count for every
// executor group family with autoscaling enabled.
func desiredGroupCounts(cluster *impalav1alpha1.ImpalaCluster) map[string]int32 {
	counts := map[string]int32{}
	status := map[string]impalav1alpha1.ExecutorGroupStatus{}
	for _, s := range cluster.Status.ExecutorGroups {
		status[s.Name] = s
	}
	for i := range cluster.Spec.ExecutorGroups {
		eg := &cluster.Spec.ExecutorGroups[i]
		if eg.Autoscaling == nil || !eg.Autoscaling.Enabled {
			continue
		}
		n := clampGroups(eg, resources.SpecGroups(eg))
		if s, ok := status[eg.Name]; ok && s.LastScaleTime != nil {
			n = clampGroups(eg, s.DesiredGroups)
		}
		counts[eg.Name] = n
	}
	return counts
}

func clampGroups(eg *impalav1alpha1.ExecutorGroupSpec, n int32) int32 {
	as := eg.Autoscaling
	if as == nil {
		return n
	}
	if as.MinGroups != nil && n < *as.MinGroups {
		n = *as.MinGroups
	}
	if as.MaxGroups != nil && n > *as.MaxGroups {
		n = *as.MaxGroups
	}
	return n
}

// referencedDataHash digests the contents of every referenced Secret and
// ConfigMap so that rotating them rolls the pods that mount them.
func (r *ImpalaClusterReconciler) referencedDataHash(ctx context.Context, cluster *impalav1alpha1.ImpalaCluster) (string, error) {
	h := sha256.New()
	for _, name := range resources.ReferencedSecrets(cluster) {
		s := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: name}, s); err != nil {
			if apierrors.IsNotFound(err) {
				return "", fmt.Errorf("referenced Secret %q not found", name)
			}
			return "", err
		}
		writeSorted(h, name, s.Data)
	}
	for _, name := range resources.ReferencedConfigMaps(cluster) {
		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: name}, cm); err != nil {
			if apierrors.IsNotFound(err) {
				return "", fmt.Errorf("referenced ConfigMap %q not found", name)
			}
			return "", err
		}
		data := map[string][]byte{}
		for k, v := range cm.Data {
			data[k] = []byte(v)
		}
		writeSorted(h, name, data)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func writeSorted(h interface{ Write([]byte) (int, error) }, name string, data map[string][]byte) {
	keys := slices.Sorted(maps.Keys(data))
	_, _ = h.Write([]byte(name))
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write(data[k])
	}
}

func setCondition(cluster *impalav1alpha1.ImpalaCluster, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cluster.Generation,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImpalaClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(ctx, &impalav1alpha1.ImpalaCluster{}, indexSecretRefs, func(o client.Object) []string {
		return resources.ReferencedSecrets(o.(*impalav1alpha1.ImpalaCluster))
	}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &impalav1alpha1.ImpalaCluster{}, indexConfigMapRefs, func(o client.Object) []string {
		return resources.ReferencedConfigMaps(o.(*impalav1alpha1.ImpalaCluster))
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&impalav1alpha1.ImpalaCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.clustersReferencing(indexSecretRefs))).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.clustersReferencing(indexConfigMapRefs))).
		Named("impalacluster").
		Complete(r)
}

// clustersReferencing maps a Secret or ConfigMap to the clusters that reference it.
func (r *ImpalaClusterReconciler) clustersReferencing(index string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		var list impalav1alpha1.ImpalaClusterList
		if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace()), client.MatchingFields{index: obj.GetName()}); err != nil {
			return nil
		}
		reqs := make([]ctrl.Request, 0, len(list.Items))
		for i := range list.Items {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
		return reqs
	}
}
