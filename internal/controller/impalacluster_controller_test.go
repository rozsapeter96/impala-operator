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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
	"github.com/rozsapeter96/impala-operator/internal/resources"
)

var _ = Describe("ImpalaCluster controller", func() {
	const namespace = "default"

	newCluster := func(name string) *impalav1alpha1.ImpalaCluster {
		return &impalav1alpha1.ImpalaCluster{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: impalav1alpha1.ImpalaClusterSpec{
				ClusterConfig: impalav1alpha1.ClusterConfig{
					HiveMetastore: &impalav1alpha1.HiveMetastoreSpec{URIs: "thrift://hms:9083"},
				},
				Coordinators: impalav1alpha1.CoordinatorSpec{Replicas: new(int32(1))},
				ExecutorGroups: []impalav1alpha1.ExecutorGroupSpec{{
					Name:   "small",
					Size:   2,
					Groups: new(int32(2)),
				}},
			},
		}
	}

	getSTS := func(ctx context.Context, name string) (*appsv1.StatefulSet, error) {
		sts := &appsv1.StatefulSet{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts)
		return sts, err
	}

	It("creates every managed object and reports status", func(ctx SpecContext) {
		cluster := newCluster("demo")
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

		for _, name := range []string{"demo-statestore", "demo-catalog", "demo-coordinator", "demo-exec-small-0", "demo-exec-small-1"} {
			Eventually(func() error { _, err := getSTS(ctx, name); return err }).WithContext(ctx).Should(Succeed(), name)
		}
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "demo-conf", Namespace: namespace}, cm)).To(Succeed())
		Expect(cm.Data).To(HaveKey("hive-site.xml"))
		Expect(metav1.IsControlledBy(cm, cluster)).To(BeTrue())

		pdb := &policyv1.PodDisruptionBudget{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "demo-coordinator", Namespace: namespace}, pdb)).To(Succeed())

		Eventually(func(g Gomega) {
			live := &impalav1alpha1.ImpalaCluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), live)).To(Succeed())
			g.Expect(live.Status.ObservedGeneration).To(Equal(live.Generation))
			g.Expect(live.Status.ExecutorGroups).To(HaveLen(1))
			g.Expect(live.Status.ExecutorGroups[0].DesiredGroups).To(Equal(int32(2)))
			g.Expect(live.Status.ExecutorGroups[0].Groups).To(HaveLen(2))
			g.Expect(live.Status.ExecutorGroups[0].Groups[0].Name).To(Equal("default-pool-small-0"))
			g.Expect(live.Status.Endpoints.HiveServer2).To(Equal("demo-coordinator.default.svc:21050"))
			g.Expect(live.Status.Endpoints.WebUI).To(Equal("demo-coordinator-hl.default.svc:25000"))
			g.Expect(meta.IsStatusConditionFalse(live.Status.Conditions, impalav1alpha1.ConditionDegraded)).To(BeTrue())
			// No pods run in envtest, so the cluster stays not-ready.
			g.Expect(meta.IsStatusConditionFalse(live.Status.Conditions, impalav1alpha1.ConditionReady)).To(BeTrue())
		}).WithContext(ctx).Should(Succeed())
	})

	It("removes executor groups that are scaled away and rolls on config change", func(ctx SpecContext) {
		cluster := newCluster("scale")
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

		Eventually(func() error { _, err := getSTS(ctx, "scale-exec-small-1"); return err }).WithContext(ctx).Should(Succeed())
		before, err := getSTS(ctx, "scale-exec-small-0")
		Expect(err).NotTo(HaveOccurred())
		hashBefore := before.Spec.Template.Annotations[resources.AnnotationConfigHash]
		Expect(hashBefore).NotTo(BeEmpty())

		Eventually(func(g Gomega) {
			live := &impalav1alpha1.ImpalaCluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), live)).To(Succeed())
			live.Spec.ExecutorGroups[0].Groups = new(int32(1))
			live.Spec.ClusterConfig.HiveMetastore.URIs = "thrift://other:9083"
			g.Expect(k8sClient.Update(ctx, live)).To(Succeed())
		}).WithContext(ctx).Should(Succeed())

		Eventually(func() error {
			_, err := getSTS(ctx, "scale-exec-small-1")
			if err == nil {
				return fmt.Errorf("statefulset still exists")
			}
			return client.IgnoreNotFound(err)
		}).WithContext(ctx).Should(Succeed())

		Eventually(func(g Gomega) {
			after, err := getSTS(ctx, "scale-exec-small-0")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(after.Spec.Template.Annotations[resources.AnnotationConfigHash]).NotTo(Equal(hashBefore))
		}).WithContext(ctx).Should(Succeed())
	})

	It("creates and removes the NetworkPolicy with security.networkPolicy", func(ctx SpecContext) {
		cluster := newCluster("netpol")
		cluster.Spec.ClusterConfig.Security.NetworkPolicy.Enabled = true
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

		pol := &networkingv1.NetworkPolicy{}
		key := types.NamespacedName{Name: "netpol-impala", Namespace: namespace}
		Eventually(func() error { return k8sClient.Get(ctx, key, pol) }).WithContext(ctx).Should(Succeed())
		Expect(metav1.IsControlledBy(pol, cluster)).To(BeTrue())
		Expect(pol.Spec.Ingress).To(HaveLen(3))
		Expect(pol.Spec.Ingress[2].From[0].NamespaceSelector.MatchLabels).To(HaveKeyWithValue(resources.NamespaceNameLabel, "impala-operator-system"))

		Eventually(func(g Gomega) {
			live := &impalav1alpha1.ImpalaCluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), live)).To(Succeed())
			live.Spec.ClusterConfig.Security.NetworkPolicy.Enabled = false
			g.Expect(k8sClient.Update(ctx, live)).To(Succeed())
		}).WithContext(ctx).Should(Succeed())
		Eventually(func() error {
			err := k8sClient.Get(ctx, key, pol)
			if err == nil {
				return fmt.Errorf("network policy still exists")
			}
			return client.IgnoreNotFound(err)
		}).WithContext(ctx).Should(Succeed())
	})

	It("rejects inconsistent security specs and PVC changes through CRD validation", func(ctx SpecContext) {
		bad := newCluster("cel-tls")
		bad.Spec.ClusterConfig.Security.TLS.Enabled = true
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("tls.certSecretRef is required")))

		bad = newCluster("cel-krb")
		bad.Spec.ClusterConfig.Security.Kerberos.Enabled = true
		bad.Spec.ClusterConfig.Security.Kerberos.Principal = "impala/_HOST@EXAMPLE.COM"
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("kerberos.principal and kerberos.keytabSecretRef are required")))

		cluster := newCluster("cel-pvc")
		cluster.Spec.ExecutorGroups[0].Config.Scratch = &impalav1alpha1.VolumeSpec{Size: resource.MustParse("1Gi")}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

		// Dropping, resizing or adding a claim template is rejected...
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
		cluster.Spec.ExecutorGroups[0].Config.Scratch = nil
		Expect(k8sClient.Update(ctx, cluster)).To(MatchError(ContainSubstring("config.scratch is immutable")))

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
		cluster.Spec.ExecutorGroups[0].Config.Scratch.Size = resource.MustParse("2Gi")
		Expect(k8sClient.Update(ctx, cluster)).To(MatchError(ContainSubstring("config.scratch is immutable")))

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
		cluster.Spec.ExecutorGroups[0].Config.DataCache = &impalav1alpha1.VolumeSpec{Size: resource.MustParse("1Gi")}
		Expect(k8sClient.Update(ctx, cluster)).To(MatchError(ContainSubstring("config.dataCache is immutable")))

		// ...while unrelated changes and a brand-new family with claims are fine.
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
		cluster.Spec.ExecutorGroups[0].Groups = new(int32(1))
		cluster.Spec.ExecutorGroups = append(cluster.Spec.ExecutorGroups, impalav1alpha1.ExecutorGroupSpec{
			Name: "big", Size: 1,
			Config: impalav1alpha1.ExecutorConfig{DataCache: &impalav1alpha1.VolumeSpec{Size: resource.MustParse("1Gi")}},
		})
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
	})

	It("runs without catalogd when only Iceberg REST catalogs are configured", func(ctx SpecContext) {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "polaris-creds", Namespace: namespace}, StringData: map[string]string{"credential": "id:secret"}}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed()) })

		cluster := newCluster("rest")
		cluster.Spec.ClusterConfig.IcebergRESTCatalogs = []impalav1alpha1.IcebergRESTCatalogSpec{{
			Name:      "polaris",
			URI:       "http://polaris:8181/api/catalog",
			Warehouse: "lake",
			OAuth2: &impalav1alpha1.RESTCatalogOAuth2Spec{
				CredentialSecretRef: impalav1alpha1.SecretKeyReference{Name: "polaris-creds"},
				Scope:               "PRINCIPAL_ROLE:ALL",
			},
		}}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

		// Hybrid mode first: HMS plus a REST catalog, catalogd deployed.
		Eventually(func() error { _, err := getSTS(ctx, "rest-catalog"); return err }).WithContext(ctx).Should(Succeed())
		Eventually(func(g Gomega) {
			coord, err := getSTS(ctx, "rest-coordinator")
			g.Expect(err).NotTo(HaveOccurred())
			args := coord.Spec.Template.Spec.Containers[0].Args
			g.Expect(args).To(ContainElement("-catalog_config_dir=/opt/impala/catalogs"))
			g.Expect(args).To(ContainElement("-catalog_service_host=rest-catalog"))
		}).WithContext(ctx).Should(Succeed())
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "rest-conf", Namespace: namespace}, cm)).To(Succeed())
		Expect(cm.Data).To(HaveKey("rest-catalog-polaris.properties"))
		Expect(cm.Data["rest-catalog-polaris.properties"]).To(ContainSubstring("${ENV:IMPALA_REST_CATALOG_POLARIS_CREDENTIAL}"))
		Expect(cm.Data["rest-catalog-polaris.properties"]).NotTo(ContainSubstring("id:secret"))

		// Dropping the metastore switches to standalone mode and removes catalogd.
		Eventually(func(g Gomega) {
			live := &impalav1alpha1.ImpalaCluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), live)).To(Succeed())
			live.Spec.ClusterConfig.HiveMetastore = nil
			g.Expect(k8sClient.Update(ctx, live)).To(Succeed())
		}).WithContext(ctx).Should(Succeed())

		Eventually(func() error {
			_, err := getSTS(ctx, "rest-catalog")
			if err == nil {
				return fmt.Errorf("catalog statefulset still exists")
			}
			return client.IgnoreNotFound(err)
		}).WithContext(ctx).Should(Succeed())
		Eventually(func(g Gomega) {
			coord, err := getSTS(ctx, "rest-coordinator")
			g.Expect(err).NotTo(HaveOccurred())
			args := coord.Spec.Template.Spec.Containers[0].Args
			g.Expect(args).To(ContainElement("-catalogd_deployed=false"))
			g.Expect(args).NotTo(ContainElement("-catalog_service_host=rest-catalog"))
			live := &impalav1alpha1.ImpalaCluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), live)).To(Succeed())
			g.Expect(live.Status.ObservedGeneration).To(Equal(live.Generation))
			g.Expect(live.Status.Catalog.Replicas).To(BeZero())
			g.Expect(meta.IsStatusConditionFalse(live.Status.Conditions, impalav1alpha1.ConditionDegraded)).To(BeTrue())
		}).WithContext(ctx).Should(Succeed())

		// A cluster with neither a metastore nor a REST catalog is rejected.
		bad := newCluster("cel-nocatalog")
		bad.Spec.ClusterConfig.HiveMetastore = nil
		Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring("clusterConfig needs a hiveMetastore")))
	})

	It("marks the cluster degraded when a referenced secret is missing", func(ctx SpecContext) {
		cluster := newCluster("nosecret")
		cluster.Spec.ClusterConfig.Storage.S3 = &impalav1alpha1.S3Spec{CredentialsSecretRef: &corev1.LocalObjectReference{Name: "missing"}}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

		Eventually(func(g Gomega) {
			live := &impalav1alpha1.ImpalaCluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), live)).To(Succeed())
			g.Expect(meta.IsStatusConditionTrue(live.Status.Conditions, impalav1alpha1.ConditionDegraded)).To(BeTrue())
		}).WithContext(ctx).Should(Succeed())

		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: namespace}, StringData: map[string]string{"AWS_ACCESS_KEY_ID": "x"}}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed()) })

		Eventually(func(g Gomega) {
			live := &impalav1alpha1.ImpalaCluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), live)).To(Succeed())
			g.Expect(meta.IsStatusConditionFalse(live.Status.Conditions, impalav1alpha1.ConditionDegraded)).To(BeTrue())
		}).WithContext(ctx).Should(Succeed())
	})
})
