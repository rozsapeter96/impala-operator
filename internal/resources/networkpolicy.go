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
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

// NamespaceNameLabel is set on every Namespace by the API server (since
// Kubernetes 1.21) and lets a NetworkPolicy select a namespace by name.
const NamespaceNameLabel = "kubernetes.io/metadata.name"

// NetworkPolicyName returns the name of the cluster's NetworkPolicy.
func NetworkPolicyName(c *impalav1alpha1.ImpalaCluster) string { return c.Name + "-impala" }

// NetworkPolicyEnabled reports whether the cluster asks for network isolation.
func NetworkPolicyEnabled(c *impalav1alpha1.ImpalaCluster) bool {
	return c.Spec.ClusterConfig.Security.NetworkPolicy.Enabled
}

// BuildNetworkPolicy renders the ingress policy for every daemon pod of the
// cluster. operatorNamespace is the namespace the operator runs in; its pods
// (the autoscaler) must reach the coordinator web port.
func BuildNetworkPolicy(c *impalav1alpha1.ImpalaCluster, operatorNamespace string) *networkingv1.NetworkPolicy {
	np := &c.Spec.ClusterConfig.Security.NetworkPolicy
	clusterPods := metav1.LabelSelector{MatchLabels: map[string]string{
		LabelName:     AppName,
		LabelInstance: c.Name,
	}}

	webPeers := make([]networkingv1.NetworkPolicyPeer, 0, 1+len(np.WebUIFrom))
	webPeers = append(webPeers, networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{NamespaceNameLabel: operatorNamespace}},
	})
	webPeers = append(webPeers, np.WebUIFrom...)

	rules := []networkingv1.NetworkPolicyIngressRule{
		// Daemons talk to each other on every port (statestore, catalog,
		// KRPC, subscriber, web).
		{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &clusterPods}}},
		// Clients reach HiveServer2 on the coordinators. An empty From admits
		// any source.
		{From: np.ClientFrom, Ports: policyPorts(PortHS2, PortHS2HTTP)},
		// The operator's autoscaler and any monitoring peers reach the web UI.
		{From: webPeers, Ports: policyPorts(PortImpaladWeb, PortStatestoreWeb, PortCatalogWeb)},
	}

	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetworkPolicyName(c),
			Namespace: c.Namespace,
			Labels:    Labels(c, "network-policy"),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: clusterPods,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     rules,
		},
	}
}

func policyPorts(ports ...int32) []networkingv1.NetworkPolicyPort {
	tcp := corev1.ProtocolTCP
	out := make([]networkingv1.NetworkPolicyPort, 0, len(ports))
	for _, p := range ports {
		port := intstr.FromInt32(p)
		out = append(out, networkingv1.NetworkPolicyPort{Protocol: &tcp, Port: &port})
	}
	return out
}
