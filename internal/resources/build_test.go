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
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

const (
	sampleName      = "demo"
	argLocalCatalog = "-use_local_catalog=true"
)

func sampleCluster() *impalav1alpha1.ImpalaCluster {
	return &impalav1alpha1.ImpalaCluster{
		ObjectMeta: metav1.ObjectMeta{Name: sampleName, Namespace: "impala"},
		Spec: impalav1alpha1.ImpalaClusterSpec{
			Image: impalav1alpha1.ImageSpec{Repository: "apache/impala", Version: "4.5.2"},
			ClusterConfig: impalav1alpha1.ClusterConfig{
				HiveMetastore: &impalav1alpha1.HiveMetastoreSpec{URIs: "thrift://hms:9083"},
				Storage: impalav1alpha1.StorageSpec{S3: &impalav1alpha1.S3Spec{
					Endpoint:             "http://minio:9000",
					PathStyleAccess:      true,
					CredentialsSecretRef: &corev1.LocalObjectReference{Name: "s3-creds"},
				}},
				AdmissionControl: impalav1alpha1.AdmissionControlSpec{Pools: []impalav1alpha1.PoolSpec{{
					Name:              "default",
					MaxRunningQueries: new(int32(4)),
					MaxQueuedQueries:  new(int32(20)),
					MaxMemResources:   "50000 mb",
					MaxQueryMemLimit:  "8gb",
				}}},
			},
			Coordinators: impalav1alpha1.CoordinatorSpec{
				Replicas: new(int32(2)),
				Config: impalav1alpha1.CoordinatorConfig{
					ImpaladConfig:       impalav1alpha1.ImpaladConfig{MemLimit: "4gb", GracefulShutdownDeadlineSeconds: new(int32(600))},
					DefaultQueryOptions: map[string]string{"mt_dop": "4"},
				},
			},
			ExecutorGroups: []impalav1alpha1.ExecutorGroupSpec{{
				Name:           "small",
				Pool:           "default",
				Size:           3,
				MinHealthySize: new(int32(2)),
				Groups:         new(int32(2)),
				Config: impalav1alpha1.ExecutorConfig{
					ImpaladConfig: impalav1alpha1.ImpaladConfig{MemLimit: "8gb"},
					DataCache:     &impalav1alpha1.VolumeSpec{Size: resource.MustParse("10Gi")},
				},
			}},
		},
	}
}

func hasArg(args []string, want string) bool {
	return slices.Contains(args, want)
}

func TestBuildProducesAllObjects(t *testing.T) {
	d := Build(sampleCluster(), nil, "", "impala-operator-system")
	objs := d.AllObjects()
	names := map[string]bool{}
	for _, o := range objs {
		names[o.GetObjectKind().GroupVersionKind().Kind+"/"+o.GetName()] = true
	}
	for _, want := range []string{
		"ConfigMap/demo-conf",
		"StatefulSet/demo-statestore", "Service/demo-statestore", "Service/demo-statestore-hl",
		"StatefulSet/demo-catalog", "Service/demo-catalog",
		"StatefulSet/demo-coordinator", "Service/demo-coordinator", "Service/demo-coordinator-hl", "PodDisruptionBudget/demo-coordinator",
		"Service/demo-exec-small-hl",
		"StatefulSet/demo-exec-small-0", "StatefulSet/demo-exec-small-1", "PodDisruptionBudget/demo-exec-small-1",
	} {
		if !names[want] {
			t.Errorf("missing object %s; got %v", want, names)
		}
	}
	if names["StatefulSet/demo-exec-small-2"] {
		t.Errorf("unexpected third executor group instance")
	}
}

func TestGroupCountOverride(t *testing.T) {
	d := Build(sampleCluster(), map[string]int32{"small": 3}, "", "impala-operator-system")
	if got := len(d.ExecutorGroups[0].Instances); got != 3 {
		t.Fatalf("expected 3 instances, got %d", got)
	}
}

func TestConfigMapContents(t *testing.T) {
	cm := BuildConfigMap(sampleCluster())
	hive := cm.Data[FileHiveSite]
	if !strings.Contains(hive, "<name>hive.metastore.uris</name>") || !strings.Contains(hive, "<value>thrift://hms:9083</value>") {
		t.Errorf("hive-site missing metastore uri:\n%s", hive)
	}
	core := cm.Data[FileCoreSite]
	for _, want := range []string{"fs.s3a.endpoint", "http://minio:9000", "fs.s3a.path.style.access", "EnvironmentVariableCredentialsProvider", "fs.s3a.connection.ssl.enabled"} {
		if !strings.Contains(core, want) {
			t.Errorf("core-site missing %q:\n%s", want, core)
		}
	}
	fs := cm.Data[FileFairScheduler]
	for _, want := range []string{`<queue name="root">`, `<queue name="default">`, "50000 mb, 0 vcores",
		`<rule name="specified" create="false">`, `<rule name="default" queue="root.default">`} {
		if !strings.Contains(fs, want) {
			t.Errorf("fair-scheduler missing %q:\n%s", want, fs)
		}
	}
	llama := cm.Data[FileLlamaSite]
	for _, want := range []string{
		"llama.am.throttling.maximum.placed.reservations.root.default</name>", "<value>4</value>",
		"llama.am.throttling.maximum.queued.reservations.root.default", "<value>20</value>",
		"impala.admission-control.max-query-mem-limit.root.default", "<value>8gb</value>",
	} {
		if !strings.Contains(llama, want) {
			t.Errorf("llama-site missing %q:\n%s", want, llama)
		}
	}
}

func TestConfigHashStable(t *testing.T) {
	a := ConfigHash(BuildConfigMap(sampleCluster()))
	b := ConfigHash(BuildConfigMap(sampleCluster()))
	if a != b {
		t.Fatalf("hash not stable: %s vs %s", a, b)
	}
	c := sampleCluster()
	c.Spec.ClusterConfig.HiveMetastore.URIs = "thrift://other:9083"
	if ConfigHash(BuildConfigMap(c)) == a {
		t.Fatalf("hash did not change with config")
	}
}

func TestCoordinatorStatefulSet(t *testing.T) {
	c := sampleCluster()
	sts, svcs := BuildCoordinators(c, "abc")
	if *sts.Spec.Replicas != 2 {
		t.Errorf("replicas = %d", *sts.Spec.Replicas)
	}
	ctr := sts.Spec.Template.Spec.Containers[0]
	if ctr.Image != "apache/impala:4.5.2-impalad_coordinator" {
		t.Errorf("image = %s", ctr.Image)
	}
	for _, want := range []string{
		"-is_coordinator=true", "-is_executor=false", argLocalCatalog,
		"-state_store_host=demo-statestore", "-catalog_service_host=demo-catalog",
		"-hostname=$(POD_NAME).demo-coordinator-hl.impala.svc.cluster.local",
		"-use_resolved_hostname=false", "-mem_limit=4gb",
		"-default_query_options=mt_dop=4",
		"-fair_scheduler_allocation_path=/opt/impala/conf/fair-scheduler.xml",
		"-llama_site_path=/opt/impala/conf/llama-site.xml",
		"-shutdown_deadline_s=600", "-shutdown_grace_period_s=30",
		"-num_expected_executors=3",
	} {
		if !hasArg(ctr.Args, want) {
			t.Errorf("missing arg %q in %v", want, ctr.Args)
		}
	}
	if sts.Spec.Template.Annotations[AnnotationConfigHash] != "abc" {
		t.Errorf("config hash annotation missing")
	}
	if ctr.Lifecycle == nil || ctr.Lifecycle.PreStop == nil || !strings.Contains(ctr.Lifecycle.PreStop.Exec.Command[2], "RTMIN") {
		t.Errorf("preStop hook must send SIGRTMIN")
	}
	if got := *sts.Spec.Template.Spec.TerminationGracePeriodSeconds; got != 600+30+60 {
		t.Errorf("terminationGracePeriodSeconds = %d", got)
	}
	if len(ctr.EnvFrom) != 1 || ctr.EnvFrom[0].SecretRef.Name != "s3-creds" {
		t.Errorf("s3 credentials not injected: %+v", ctr.EnvFrom)
	}
	if ctr.ReadinessProbe.HTTPGet.Path != "/healthz" || ctr.ReadinessProbe.HTTPGet.Port.IntValue() != int(PortImpaladWeb) {
		t.Errorf("readiness probe wrong: %+v", ctr.ReadinessProbe.HTTPGet)
	}
	if *sts.Spec.Template.Spec.SecurityContext.RunAsUser != 1000 {
		t.Errorf("must run as uid 1000")
	}
	if len(svcs) != 2 || svcs[0].Spec.ClusterIP != corev1.ClusterIPNone || !svcs[0].Spec.PublishNotReadyAddresses {
		t.Errorf("headless service misconfigured")
	}
}

func TestExecutorGroupStatefulSet(t *testing.T) {
	c := sampleCluster()
	eg := &c.Spec.ExecutorGroups[0]
	sts := BuildExecutorGroup(c, eg, 1, "h")
	if sts.Name != "demo-exec-small-1" {
		t.Errorf("name = %s", sts.Name)
	}
	if *sts.Spec.Replicas != 3 {
		t.Errorf("replicas = %d", *sts.Spec.Replicas)
	}
	ctr := sts.Spec.Template.Spec.Containers[0]
	if ctr.Image != "apache/impala:4.5.2-impalad_executor" {
		t.Errorf("image = %s", ctr.Image)
	}
	for _, want := range []string{
		"-is_coordinator=false", "-is_executor=true",
		"-executor_groups=root.default-small-1:2",
		"-scratch_dirs=/opt/impala/scratch",
		"-data_cache=/opt/impala/cache:9216MB",
		"-hostname=$(POD_NAME).demo-exec-small-hl.impala.svc.cluster.local",
	} {
		if !hasArg(ctr.Args, want) {
			t.Errorf("missing arg %q in %v", want, ctr.Args)
		}
	}
	if len(sts.Spec.VolumeClaimTemplates) != 1 || sts.Spec.VolumeClaimTemplates[0].Name != "cache" {
		t.Errorf("expected one PVC template for cache, got %+v", sts.Spec.VolumeClaimTemplates)
	}
	if sts.Spec.Selector.MatchLabels[LabelExecutorGroupIndex] != "1" {
		t.Errorf("selector must pin the group index")
	}
	if sts.Spec.PersistentVolumeClaimRetentionPolicy == nil {
		t.Errorf("PVC retention policy must delete disposable volumes")
	}
}

func TestDefaultPoolWhenNoPools(t *testing.T) {
	c := sampleCluster()
	c.Spec.ClusterConfig.AdmissionControl.Pools = nil
	eg := &c.Spec.ExecutorGroups[0]
	if got := ImpalaExecutorGroupName(c, eg, 0); got != "default-pool-small-0" {
		t.Errorf("group name = %s", got)
	}
	sts, _ := BuildCoordinators(c, "h")
	if hasArg(sts.Spec.Template.Spec.Containers[0].Args, "-llama_site_path=/opt/impala/conf/llama-site.xml") {
		t.Errorf("admission config paths must not be set without pools")
	}
	if _, ok := BuildConfigMap(c).Data[FileFairScheduler]; ok {
		t.Errorf("fair-scheduler.xml must not be rendered without pools")
	}
}

func TestCatalogHA(t *testing.T) {
	c := sampleCluster()
	c.Spec.Catalog.Replicas = new(int32(2))
	cat, _ := BuildCatalog(c, "h")
	ss, _ := BuildStatestore(c, "h")
	if !hasArg(cat.Spec.Template.Spec.Containers[0].Args, "-enable_catalogd_ha=true") {
		t.Errorf("catalogd HA flag missing")
	}
	if !hasArg(ss.Spec.Template.Spec.Containers[0].Args, "-enable_catalogd_ha=true") {
		t.Errorf("statestored must also get the catalogd HA flag")
	}
}

func TestSecurityFlags(t *testing.T) {
	c := sampleCluster()
	c.Spec.ClusterConfig.Security = impalav1alpha1.SecuritySpec{
		TLS:      impalav1alpha1.TLSSpec{Enabled: true, CertSecretRef: &corev1.LocalObjectReference{Name: "tls"}},
		Kerberos: impalav1alpha1.KerberosSpec{Enabled: true, Principal: "impala/_HOST@EXAMPLE.COM", KeytabSecretRef: &corev1.LocalObjectReference{Name: "kt"}, Krb5ConfigMapRef: &corev1.LocalObjectReference{Name: "krb5"}},
		LDAP:     impalav1alpha1.LDAPSpec{Enabled: true, URI: "ldaps://ldap", BindPattern: "uid=#UID,dc=x"},
	}
	coord, _ := BuildCoordinators(c, "h")
	ctr := coord.Spec.Template.Spec.Containers[0]
	for _, want := range []string{
		"-ssl_server_certificate=/opt/impala/secrets/tls/tls.crt",
		"-ssl_private_key=/opt/impala/secrets/tls/tls.key",
		"-ssl_client_ca_certificate=/opt/impala/secrets/tls/ca.crt",
		"-webserver_certificate_file=/opt/impala/secrets/tls/tls.crt",
		"-principal=impala/_HOST@EXAMPLE.COM",
		"-keytab_file=/opt/impala/secrets/kerberos/impala.keytab",
		"-krb5_conf=/opt/impala/secrets/kerberos/krb5.conf",
		"-enable_ldap_auth=true", "-ldap_uri=ldaps://ldap", "-ldap_bind_pattern=uid=#UID,dc=x",
	} {
		if !hasArg(ctr.Args, want) {
			t.Errorf("missing arg %q", want)
		}
	}
	if ctr.ReadinessProbe.HTTPGet.Scheme != corev1.URISchemeHTTPS {
		t.Errorf("probes must use HTTPS when TLS is on")
	}
	exec := BuildExecutorGroup(c, &c.Spec.ExecutorGroups[0], 0, "h")
	if hasArg(exec.Spec.Template.Spec.Containers[0].Args, "-enable_ldap_auth=true") {
		t.Errorf("executors must not get LDAP flags")
	}
	ss, _ := BuildStatestore(c, "h")
	if !hasArg(ss.Spec.Template.Spec.Containers[0].Args, "-ssl_client_ca_certificate=/opt/impala/secrets/tls/ca.crt") {
		t.Errorf("internal TLS must reach the statestore")
	}
	refs := ReferencedSecrets(c)
	if len(refs) != 3 {
		t.Errorf("expected 3 referenced secrets, got %v", refs)
	}
}

func TestUserArgsOverride(t *testing.T) {
	c := sampleCluster()
	c.Spec.Coordinators.Args = []string{"-mem_limit=1gb"}
	sts, _ := BuildCoordinators(c, "h")
	args := sts.Spec.Template.Spec.Containers[0].Args
	if args[len(args)-1] != "-mem_limit=1gb" {
		t.Errorf("user args must come last: %v", args)
	}
}

// The debug web UI is unauthenticated, so it must not be reachable through a
// client Service (which may be a LoadBalancer); only the headless Services
// carry it.
func TestWebUINotOnClientServices(t *testing.T) {
	d := Build(sampleCluster(), nil, "", "impala-operator-system")
	for _, o := range d.AllObjects() {
		svc, ok := o.(*corev1.Service)
		if !ok {
			continue
		}
		headless := svc.Spec.ClusterIP == corev1.ClusterIPNone
		hasWeb := false
		for _, p := range svc.Spec.Ports {
			if p.Name == "web" {
				hasWeb = true
			}
		}
		if hasWeb && !headless {
			t.Errorf("client Service %s exposes the web UI port", svc.Name)
		}
		if !hasWeb && headless {
			t.Errorf("headless Service %s should carry the web UI port", svc.Name)
		}
	}
}

func TestServiceAccountTokenNotMountedByDefault(t *testing.T) {
	c := sampleCluster()
	d := Build(c, nil, "", "impala-operator-system")
	for _, tier := range []Tier{d.Statestore, d.Catalog, d.Coordinators, d.ExecutorGroups[0].Instances[0]} {
		am := tier.StatefulSet.Spec.Template.Spec.AutomountServiceAccountToken
		if am == nil || *am {
			t.Errorf("%s: service account token must not be mounted by default", tier.Name)
		}
	}

	// A dedicated ServiceAccount implies the token is wanted (IAM bindings,
	// Vault agents) unless the user says otherwise.
	c.Spec.Coordinators.PodOverrides.ServiceAccountName = "impala"
	sts, _ := BuildCoordinators(c, "h")
	if sts.Spec.Template.Spec.AutomountServiceAccountToken != nil {
		t.Errorf("serviceAccountName without an explicit automount should leave the default")
	}
	c.Spec.Coordinators.PodOverrides.AutomountServiceAccountToken = new(false)
	sts, _ = BuildCoordinators(c, "h")
	if am := sts.Spec.Template.Spec.AutomountServiceAccountToken; am == nil || *am {
		t.Errorf("explicit automount=false must win")
	}
}

func TestNetworkPolicy(t *testing.T) {
	c := sampleCluster()
	if d := Build(c, nil, "", "ops"); d.NetworkPolicy != nil {
		t.Fatalf("network policy must be opt-in")
	}
	np := &c.Spec.ClusterConfig.Security.NetworkPolicy
	np.Enabled = true
	np.ClientFrom = []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "analytics"}}}}
	np.WebUIFrom = []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"name": "monitoring"}}}}
	d := Build(c, nil, "", "ops")
	pol := d.NetworkPolicy
	if pol == nil {
		t.Fatalf("expected a network policy")
	}
	if pol.Spec.PodSelector.MatchLabels[LabelInstance] != sampleName || pol.Spec.PodSelector.MatchLabels[LabelName] != AppName {
		t.Errorf("policy must select every pod of the cluster: %v", pol.Spec.PodSelector)
	}
	if len(pol.Spec.PolicyTypes) != 1 || pol.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("only ingress must be restricted: %v", pol.Spec.PolicyTypes)
	}
	if len(pol.Spec.Ingress) != 3 {
		t.Fatalf("expected 3 ingress rules, got %d", len(pol.Spec.Ingress))
	}
	intra, clients, web := pol.Spec.Ingress[0], pol.Spec.Ingress[1], pol.Spec.Ingress[2]
	if len(intra.Ports) != 0 || len(intra.From) != 1 || intra.From[0].PodSelector == nil || intra.From[0].PodSelector.MatchLabels[LabelInstance] != sampleName {
		t.Errorf("cluster pods must reach every port of each other: %+v", intra)
	}
	if len(clients.From) != 1 || clients.From[0].NamespaceSelector.MatchLabels["team"] != "analytics" {
		t.Errorf("client rule must use clientFrom: %+v", clients.From)
	}
	if ports := portNumbers(clients.Ports); !slices.Equal(ports, []int32{PortHS2, PortHS2HTTP}) {
		t.Errorf("client rule ports = %v", ports)
	}
	if len(web.From) != 2 || web.From[0].NamespaceSelector.MatchLabels[NamespaceNameLabel] != "ops" || web.From[1].NamespaceSelector.MatchLabels["name"] != "monitoring" {
		t.Errorf("web rule must admit the operator namespace and webUIFrom: %+v", web.From)
	}
	if ports := portNumbers(web.Ports); !slices.Equal(ports, []int32{PortImpaladWeb, PortStatestoreWeb, PortCatalogWeb}) {
		t.Errorf("web rule ports = %v", ports)
	}

	// Without clientFrom the client ports are open to any source.
	np.ClientFrom = nil
	pol = Build(c, nil, "", "ops").NetworkPolicy
	if len(pol.Spec.Ingress[1].From) != 0 {
		t.Errorf("empty clientFrom must admit any source")
	}
}

func portNumbers(ports []networkingv1.NetworkPolicyPort) []int32 {
	out := make([]int32, 0, len(ports))
	for _, p := range ports {
		out = append(out, p.Port.IntVal)
	}
	return out
}

func polarisCluster() *impalav1alpha1.ImpalaCluster {
	c := sampleCluster()
	c.Spec.ClusterConfig.IcebergRESTCatalogs = []impalav1alpha1.IcebergRESTCatalogSpec{{
		Name:      "polaris",
		URI:       "http://polaris:8181/api/catalog",
		Warehouse: "lake",
		OAuth2: &impalav1alpha1.RESTCatalogOAuth2Spec{
			CredentialSecretRef: impalav1alpha1.SecretKeyReference{Name: "polaris-creds"},
			Scope:               "PRINCIPAL_ROLE:ALL",
		},
		Properties: map[string]string{"io-impl": "org.apache.iceberg.hadoop.HadoopFileIO"},
	}}
	return c
}

func TestRESTCatalogAlongsideHMS(t *testing.T) {
	c := polarisCluster()
	cm := BuildConfigMap(c)
	props := cm.Data["rest-catalog-polaris.properties"]
	for _, want := range []string{
		"connector.name=iceberg\n",
		"iceberg.catalog.type=rest\n",
		"iceberg.rest-catalog.name=polaris\n",
		"iceberg.rest-catalog.uri=http://polaris:8181/api/catalog\n",
		"iceberg.rest-catalog.warehouse=lake\n",
		"iceberg.rest-catalog.security=OAUTH2\n",
		"iceberg.rest-catalog.oauth2.credential=${ENV:IMPALA_REST_CATALOG_POLARIS_CREDENTIAL}\n",
		"iceberg.rest-catalog.oauth2.scope=PRINCIPAL_ROLE:ALL\n",
		"iceberg.rest-catalog.oauth2.server-uri=http://polaris:8181/api/catalog/v1/oauth/tokens\n",
		"io-impl=org.apache.iceberg.hadoop.HadoopFileIO\n",
	} {
		if !strings.Contains(props, want) {
			t.Errorf("properties missing %q:\n%s", want, props)
		}
	}
	if strings.Contains(props, "vended-credentials") {
		t.Errorf("vended credentials must be off by default:\n%s", props)
	}
	if !strings.Contains(cm.Data[FileHiveSite], "hive.metastore.uris") {
		t.Errorf("HMS must still be configured in hybrid mode")
	}

	coord, _ := BuildCoordinators(c, "h")
	pod := coord.Spec.Template.Spec
	ctr := pod.Containers[0]
	for _, want := range []string{"-catalog_config_dir=/opt/impala/catalogs", "-catalog_service_host=demo-catalog", argLocalCatalog} {
		if !hasArg(ctr.Args, want) {
			t.Errorf("missing arg %q in %v", want, ctr.Args)
		}
	}
	if hasArg(ctr.Args, "-catalogd_deployed=false") {
		t.Errorf("catalogd is deployed in hybrid mode")
	}
	var cred *corev1.EnvVar
	for i := range ctr.Env {
		if ctr.Env[i].Name == "IMPALA_REST_CATALOG_POLARIS_CREDENTIAL" {
			cred = &ctr.Env[i]
		}
	}
	if cred == nil || cred.ValueFrom == nil || cred.ValueFrom.SecretKeyRef == nil ||
		cred.ValueFrom.SecretKeyRef.Name != "polaris-creds" || cred.ValueFrom.SecretKeyRef.Key != "credential" {
		t.Errorf("credential env not injected from the Secret: %+v", cred)
	}
	var vol *corev1.Volume
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == "catalogs" {
			vol = &pod.Volumes[i]
		}
	}
	if vol == nil || vol.ConfigMap == nil || vol.ConfigMap.Name != "demo-conf" ||
		len(vol.ConfigMap.Items) != 1 || vol.ConfigMap.Items[0].Key != "rest-catalog-polaris.properties" || vol.ConfigMap.Items[0].Path != "polaris.properties" {
		t.Errorf("catalog properties must be projected alone into the catalog dir: %+v", vol)
	}
	mounted := false
	for _, m := range ctr.VolumeMounts {
		if m.Name == "catalogs" && m.MountPath == CatalogConfigDir && m.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("catalog dir not mounted: %+v", ctr.VolumeMounts)
	}
}

func TestRESTCatalogSecretsStayOnCoordinators(t *testing.T) {
	c := polarisCluster()
	if refs := ReferencedSecrets(c); !slices.Contains(refs, "polaris-creds") {
		t.Errorf("OAuth2 credential Secret must roll pods on rotation: %v", refs)
	}
	exec := BuildExecutorGroup(c, &c.Spec.ExecutorGroups[0], 0, "h")
	ectr := exec.Spec.Template.Spec.Containers[0]
	if hasArg(ectr.Args, "-catalog_config_dir=/opt/impala/catalogs") {
		t.Errorf("executors do not plan queries and must not get the catalog dir")
	}
	for _, e := range ectr.Env {
		if e.Name == "IMPALA_REST_CATALOG_POLARIS_CREDENTIAL" {
			t.Errorf("executors must not receive catalog credentials")
		}
	}
}

func TestRESTCatalogStandalone(t *testing.T) {
	c := polarisCluster()
	c.Spec.ClusterConfig.HiveMetastore = nil
	c.Spec.Catalog.Replicas = new(int32(2))

	d := Build(c, nil, "", "impala-operator-system")
	if d.Catalog.StatefulSet != nil {
		t.Fatalf("catalogd must not be deployed without a Hive Metastore")
	}
	if got := len(d.CoreTiers()); got != 2 {
		t.Errorf("expected statestore and coordinators only, got %d tiers", got)
	}
	for _, o := range d.AllObjects() {
		if o == nil {
			t.Fatalf("AllObjects returned a nil object")
		}
		if strings.HasPrefix(o.GetName(), "demo-catalog") {
			t.Errorf("unexpected catalog object %s", o.GetName())
		}
	}
	if strings.Contains(d.ConfigMap.Data[FileHiveSite], "hive.metastore.uris") {
		t.Errorf("hive-site must not point at a metastore:\n%s", d.ConfigMap.Data[FileHiveSite])
	}

	ctr := d.Coordinators.StatefulSet.Spec.Template.Spec.Containers[0]
	for _, want := range []string{"-catalogd_deployed=false", argLocalCatalog, "-catalog_config_dir=/opt/impala/catalogs"} {
		if !hasArg(ctr.Args, want) {
			t.Errorf("missing arg %q in %v", want, ctr.Args)
		}
	}
	for _, a := range ctr.Args {
		if strings.HasPrefix(a, "-catalog_service_host=") {
			t.Errorf("no catalog service to point at: %s", a)
		}
	}
	ectr := d.ExecutorGroups[0].Instances[0].StatefulSet.Spec.Template.Spec.Containers[0]
	if !hasArg(ectr.Args, "-catalogd_deployed=false") {
		t.Errorf("executors must also learn that no catalogd exists: %v", ectr.Args)
	}
	ss := d.Statestore.StatefulSet.Spec.Template.Spec.Containers[0]
	if hasArg(ss.Args, "-enable_catalogd_ha=true") {
		t.Errorf("catalog HA flag is meaningless without catalogd")
	}
}

func TestJavaProperties(t *testing.T) {
	got := javaProperties(map[string]string{
		"b key=1": " v:1\\x\n",
		"a":       "",
	})
	want := "a=\nb\\ key\\=1=\\ v:1\\\\x\\n\n"
	if got != want {
		t.Errorf("javaProperties =\n%q\nwant\n%q", got, want)
	}
}
