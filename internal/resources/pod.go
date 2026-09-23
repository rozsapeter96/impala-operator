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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

const (
	impalaUID             int64 = 1000
	containerName               = "impala"
	volumeConf                  = "conf"
	volumeLogs                  = "logs"
	volumeScratch               = "scratch"
	volumeCache                 = "cache"
	volumeTLS                   = "tls"
	volumeKerberos              = "kerberos"
	volumeLDAP                  = "ldap"
	envPodName                  = "POD_NAME"
	envPodNamespace             = "POD_NAMESPACE"
	envJavaToolOptions          = "JAVA_TOOL_OPTIONS"
	defaultShutdownGrace        = 30
	defaultShutdownDeadln       = 900
	shutdownSlackSeconds        = 60
)

// daemon describes one StatefulSet-backed Impala daemon set.
type daemon struct {
	component     Component
	name          string
	headless      string
	imageSuffix   string
	replicas      int32
	labels        map[string]string
	selector      map[string]string
	args          []string
	ports         []corev1.ContainerPort
	webPort       int32
	spec          impalav1alpha1.ComponentSpec
	jvmHeap       string
	shutdown      *impalav1alpha1.ImpaladConfig // nil for statestore/catalog
	claims        []corev1.PersistentVolumeClaim
	extraVolumes  []corev1.Volume
	extraMounts   []corev1.VolumeMount
	configHash    string
	startupBudget int32 // startup probe failure threshold (x10s)
}

// buildStatefulSet renders a StatefulSet for a daemon.
func buildStatefulSet(c *impalav1alpha1.ImpalaCluster, d daemon) *appsv1.StatefulSet {
	verbosity := int32(1)
	if v := c.Spec.ClusterConfig.Logging.Verbosity; v != nil {
		verbosity = *v
	}

	args := []string{
		"-redirect_stdout_stderr=false",
		"-logtostderr=true",
		fmt.Sprintf("-v=%d", verbosity),
		"-use_resolved_hostname=false",
		"-hostname=" + PodFQDN("$("+envPodName+")", d.headless, c.Namespace),
		fmt.Sprintf("-webserver_port=%d", d.webPort),
	}
	args = append(args, d.args...)
	args = append(args, securityArgs(c, d.component)...)
	if d.shutdown != nil {
		args = append(args,
			fmt.Sprintf("-shutdown_grace_period_s=%d", shutdownGrace(d.shutdown)),
			fmt.Sprintf("-shutdown_deadline_s=%d", shutdownDeadline(d.shutdown)),
		)
	}
	// User-supplied args go last so they override anything generated.
	args = append(args, d.spec.Args...)

	env := []corev1.EnvVar{
		{Name: envPodName, ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		{Name: envPodNamespace, ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
	}
	if d.jvmHeap != "" {
		env = append(env, corev1.EnvVar{Name: envJavaToolOptions, Value: "-Xmx" + d.jvmHeap})
	}
	if KerberosEnabled(c) && c.Spec.ClusterConfig.Security.Kerberos.Krb5ConfigMapRef != nil {
		env = append(env, corev1.EnvVar{Name: "KRB5_CONFIG", Value: Krb5File})
	}
	env = append(env, d.spec.Env...)

	var envFrom []corev1.EnvFromSource
	if s3 := c.Spec.ClusterConfig.Storage.S3; s3 != nil && s3.CredentialsSecretRef != nil {
		envFrom = append(envFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: *s3.CredentialsSecretRef}})
	}

	volumes := make([]corev1.Volume, 0, 2+len(d.extraVolumes)+len(d.spec.PodOverrides.Volumes))
	volumes = append(volumes,
		corev1.Volume{Name: volumeConf, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: ConfigMapName(c)}}}},
		corev1.Volume{Name: volumeLogs, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	)
	mounts := make([]corev1.VolumeMount, 0, 2+len(d.extraMounts)+len(d.spec.PodOverrides.VolumeMounts))
	mounts = append(mounts,
		corev1.VolumeMount{Name: volumeConf, MountPath: ConfDir, ReadOnly: true},
		corev1.VolumeMount{Name: volumeLogs, MountPath: LogDir},
	)
	secVols, secMounts := securityVolumes(c, d.component)
	volumes = append(volumes, secVols...)
	mounts = append(mounts, secMounts...)
	volumes = append(volumes, d.extraVolumes...)
	mounts = append(mounts, d.extraMounts...)
	volumes = append(volumes, d.spec.PodOverrides.Volumes...)
	mounts = append(mounts, d.spec.PodOverrides.VolumeMounts...)

	scheme := corev1.URISchemeHTTP
	if TLSEnabled(c) {
		scheme = corev1.URISchemeHTTPS
	}
	healthz := corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(d.webPort), Scheme: scheme}}
	startupBudget := d.startupBudget
	if startupBudget == 0 {
		startupBudget = 30
	}

	container := corev1.Container{
		Name:            containerName,
		Image:           Image(c, d.imageSuffix),
		ImagePullPolicy: c.Spec.Image.PullPolicy,
		Args:            args,
		Env:             env,
		EnvFrom:         envFrom,
		Ports:           d.ports,
		VolumeMounts:    mounts,
		Resources:       d.spec.Resources,
		StartupProbe:    &corev1.Probe{ProbeHandler: healthz, PeriodSeconds: 10, FailureThreshold: startupBudget},
		ReadinessProbe:  &corev1.Probe{ProbeHandler: healthz, PeriodSeconds: 10, FailureThreshold: 3},
		LivenessProbe:   &corev1.Probe{ProbeHandler: healthz, PeriodSeconds: 30, FailureThreshold: 6},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: new(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}

	var terminationGrace int64 = 60
	if d.shutdown != nil {
		// SIGTERM kills impalad immediately, so the preStop hook starts a
		// graceful shutdown with SIGRTMIN and blocks until the process exits.
		container.Lifecycle = &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{
			Command: []string{"/bin/bash", "-c", "kill -s RTMIN 1; while kill -0 1 2>/dev/null; do sleep 2; done"},
		}}}
		terminationGrace = int64(shutdownGrace(d.shutdown) + shutdownDeadline(d.shutdown) + shutdownSlackSeconds)
	}

	po := d.spec.PodOverrides
	podLabels := merge(map[string]string{}, po.Labels, d.labels)
	podAnnotations := merge(map[string]string{}, po.Annotations, map[string]string{
		AnnotationConfigHash:   d.configHash,
		"prometheus.io/scrape": trueStr,
		"prometheus.io/port":   fmt.Sprint(d.webPort),
		"prometheus.io/path":   "/metrics_prometheus",
	})

	podSecurity := &corev1.PodSecurityContext{
		RunAsUser:      new(impalaUID),
		RunAsGroup:     new(impalaUID),
		FSGroup:        new(impalaUID),
		RunAsNonRoot:   new(true),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	if po.SecurityContext != nil {
		podSecurity = po.SecurityContext
	}

	containers := append([]corev1.Container{container}, po.Sidecars...)

	// Impala runs user SQL and native UDFs, so the daemons never get a
	// ServiceAccount token unless the user runs them under a dedicated
	// ServiceAccount (cloud IAM bindings, Vault agents) or asks explicitly.
	automount := po.AutomountServiceAccountToken
	if automount == nil && po.ServiceAccountName == "" {
		automount = new(false)
	}

	sts := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.name,
			Namespace: c.Namespace,
			Labels:    d.labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:            new(d.replicas),
			ServiceName:         d.headless,
			PodManagementPolicy: appsv1.ParallelPodManagement,
			UpdateStrategy:      appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType},
			Selector:            &metav1.LabelSelector{MatchLabels: d.selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels, Annotations: podAnnotations},
				Spec: corev1.PodSpec{
					Containers:                    containers,
					InitContainers:                po.InitContainers,
					Volumes:                       volumes,
					ImagePullSecrets:              c.Spec.Image.PullSecrets,
					SecurityContext:               podSecurity,
					TerminationGracePeriodSeconds: new(terminationGrace),
					NodeSelector:                  po.NodeSelector,
					Tolerations:                   po.Tolerations,
					Affinity:                      po.Affinity,
					TopologySpreadConstraints:     po.TopologySpreadConstraints,
					PriorityClassName:             po.PriorityClassName,
					ServiceAccountName:            po.ServiceAccountName,
					AutomountServiceAccountToken:  automount,
				},
			},
			VolumeClaimTemplates: d.claims,
		},
	}
	if len(d.claims) > 0 {
		sts.Spec.PersistentVolumeClaimRetentionPolicy = &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
			WhenDeleted: appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
			WhenScaled:  appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
		}
	}
	return sts
}

func shutdownGrace(cfg *impalav1alpha1.ImpaladConfig) int32 {
	if cfg.GracefulShutdownGracePeriodSeconds != nil {
		return *cfg.GracefulShutdownGracePeriodSeconds
	}
	return defaultShutdownGrace
}

func shutdownDeadline(cfg *impalav1alpha1.ImpaladConfig) int32 {
	if cfg.GracefulShutdownDeadlineSeconds != nil {
		return *cfg.GracefulShutdownDeadlineSeconds
	}
	return defaultShutdownDeadln
}

// buildService renders a Service selecting the given labels.
func buildService(c *impalav1alpha1.ImpalaCluster, name string, labels, selector map[string]string, ports []corev1.ServicePort, headless bool) *corev1.Service {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports:    ports,
		},
	}
	if headless {
		svc.Spec.ClusterIP = corev1.ClusterIPNone
		// Pods must be resolvable before they are ready so peers can connect
		// during startup and statestore registration.
		svc.Spec.PublishNotReadyAddresses = true
	}
	return svc
}

func servicePort(name string, port int32) corev1.ServicePort {
	return corev1.ServicePort{Name: name, Port: port, TargetPort: intstr.FromInt32(port), Protocol: corev1.ProtocolTCP}
}

func containerPort(name string, port int32) corev1.ContainerPort {
	return corev1.ContainerPort{Name: name, ContainerPort: port, Protocol: corev1.ProtocolTCP}
}

// impalaMemSpec renders a quantity in a form Impala's memory parser accepts.
func impalaMemSpec(q resource.Quantity) string {
	mb := q.Value() / (1024 * 1024)
	return fmt.Sprintf("%dMB", mb)
}
