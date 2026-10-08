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

package resources

import (
	"crypto/sha256"
	"encoding/hex"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

// Tier is one rollout stage: a StatefulSet plus its supporting objects.
type Tier struct {
	Name        string
	StatefulSet *appsv1.StatefulSet
	Objects     []client.Object // services, PDBs
}

// ExecutorGroupTier groups the StatefulSets of one executor group family.
type ExecutorGroupTier struct {
	Spec      *impalav1alpha1.ExecutorGroupSpec
	Headless  *corev1.Service
	Instances []Tier
}

// Desired is the full set of objects the operator manages for a cluster.
type Desired struct {
	ConfigMap      *corev1.ConfigMap
	ConfigHash     string
	NetworkPolicy  *networkingv1.NetworkPolicy // nil when isolation is disabled
	Statestore     Tier
	Catalog        Tier // empty (nil StatefulSet) when catalogd is not deployed
	Coordinators   Tier
	ExecutorGroups []ExecutorGroupTier
}

// CoreTiers returns the non-executor tiers in rollout order, skipping the
// catalog tier when catalogd is not deployed.
func (d *Desired) CoreTiers() []Tier {
	tiers := make([]Tier, 0, 3)
	for _, t := range []Tier{d.Statestore, d.Catalog, d.Coordinators} {
		if t.StatefulSet != nil {
			tiers = append(tiers, t)
		}
	}
	return tiers
}

// Build renders every managed object. groupCounts overrides the number of
// instances per executor group family (keyed by family name); families
// absent from the map use spec.groups. extraHash is mixed into the config
// hash so that changes to referenced Secrets also roll pods.
// operatorNamespace is where the operator runs; the NetworkPolicy admits its
// autoscaler to the coordinator web port.
func Build(c *impalav1alpha1.ImpalaCluster, groupCounts map[string]int32, extraHash, operatorNamespace string) *Desired {
	cm := BuildConfigMap(c)
	hash := ConfigHash(cm)
	if extraHash != "" {
		hash = shortHash(hash + extraHash)
	}
	d := &Desired{ConfigMap: cm, ConfigHash: hash}
	if NetworkPolicyEnabled(c) {
		d.NetworkPolicy = BuildNetworkPolicy(c, operatorNamespace)
	}

	sts, svcs := BuildStatestore(c, hash)
	d.Statestore = Tier{Name: "statestore", StatefulSet: sts, Objects: toObjects(svcs)}

	if CatalogdDeployed(c) {
		sts, svcs = BuildCatalog(c, hash)
		d.Catalog = Tier{Name: "catalog", StatefulSet: sts, Objects: toObjects(svcs)}
	}

	sts, svcs = BuildCoordinators(c, hash)
	coord := Tier{Name: "coordinators", StatefulSet: sts, Objects: toObjects(svcs)}
	coord.Objects = append(coord.Objects, BuildPDB(c, CoordinatorName(c), Labels(c, ComponentCoordinator), SelectorLabels(c, ComponentCoordinator)))
	d.Coordinators = coord

	for i := range c.Spec.ExecutorGroups {
		eg := &c.Spec.ExecutorGroups[i]
		n := SpecGroups(eg)
		if v, ok := groupCounts[eg.Name]; ok {
			n = v
		}
		egt := ExecutorGroupTier{Spec: eg, Headless: BuildExecutorGroupHeadlessService(c, eg)}
		for idx := int32(0); idx < n; idx++ {
			inst := Tier{Name: ExecutorGroupName(c, eg, idx), StatefulSet: BuildExecutorGroup(c, eg, idx, hash)}
			inst.Objects = append(inst.Objects, BuildPDB(c, inst.Name, ExecutorGroupLabels(c, eg, idx), ExecutorGroupSelectorLabels(c, eg, idx)))
			egt.Instances = append(egt.Instances, inst)
		}
		d.ExecutorGroups = append(d.ExecutorGroups, egt)
	}
	return d
}

// AllObjects flattens the desired state in rollout order.
func (d *Desired) AllObjects() []client.Object {
	objs := []client.Object{d.ConfigMap}
	if d.NetworkPolicy != nil {
		objs = append(objs, d.NetworkPolicy)
	}
	for _, t := range d.CoreTiers() {
		objs = append(objs, t.Objects...)
		objs = append(objs, t.StatefulSet)
	}
	for _, eg := range d.ExecutorGroups {
		objs = append(objs, eg.Headless)
		for _, inst := range eg.Instances {
			objs = append(objs, inst.Objects...)
			objs = append(objs, inst.StatefulSet)
		}
	}
	return objs
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func toObjects(svcs []*corev1.Service) []client.Object {
	out := make([]client.Object, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, s)
	}
	return out
}
