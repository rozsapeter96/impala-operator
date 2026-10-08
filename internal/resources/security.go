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

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

// TLSEnabled reports whether TLS is fully configured (enabled with a
// certificate Secret). The CRD enforces that enabled implies certSecretRef,
// but every consumer gates on this helper so the flags, volumes, probe scheme
// and autoscaler scrape can never disagree.
func TLSEnabled(c *impalav1alpha1.ImpalaCluster) bool {
	tls := &c.Spec.ClusterConfig.Security.TLS
	return tls.Enabled && tls.CertSecretRef != nil
}

// KerberosEnabled reports whether Kerberos is fully configured (enabled with
// a keytab Secret).
func KerberosEnabled(c *impalav1alpha1.ImpalaCluster) bool {
	krb := &c.Spec.ClusterConfig.Security.Kerberos
	return krb.Enabled && krb.KeytabSecretRef != nil
}

// securityArgs returns the TLS, Kerberos and LDAP flags for a component.
func securityArgs(c *impalav1alpha1.ImpalaCluster, component Component) []string {
	sec := &c.Spec.ClusterConfig.Security
	var args []string
	isImpalad := component == ComponentCoordinator || component == ComponentExecutor

	if tls := &sec.TLS; TLSEnabled(c) {
		internal := tls.Internal == nil || *tls.Internal
		// The web UI of every daemon is served over TLS.
		args = append(args,
			"-webserver_certificate_file="+TLSCertFile,
			"-webserver_private_key_file="+TLSKeyFile,
		)
		if isImpalad || internal {
			args = append(args,
				"-ssl_server_certificate="+TLSCertFile,
				"-ssl_private_key="+TLSKeyFile,
			)
		}
		if internal {
			args = append(args, "-ssl_client_ca_certificate="+TLSCAFile)
		}
		if tls.MinimumVersion != "" {
			args = append(args, "-ssl_minimum_version="+tls.MinimumVersion)
		}
	}

	if krb := &sec.Kerberos; KerberosEnabled(c) {
		args = append(args,
			"-principal="+krb.Principal,
			"-keytab_file="+KeytabFile,
		)
		if krb.Krb5ConfigMapRef != nil {
			args = append(args, "-krb5_conf="+Krb5File)
		}
	}

	if ldap := &sec.LDAP; ldap.Enabled && component == ComponentCoordinator {
		args = append(args, "-enable_ldap_auth=true", "-ldap_uri="+ldap.URI)
		if ldap.BindPattern != "" {
			args = append(args, "-ldap_bind_pattern="+ldap.BindPattern)
		}
		if ldap.Domain != "" {
			args = append(args, "-ldap_domain="+ldap.Domain)
		}
		if ldap.BaseDN != "" {
			args = append(args, "-ldap_baseDN="+ldap.BaseDN)
		}
		if ldap.CACertSecretRef != nil {
			args = append(args, "-ldap_ca_certificate="+LDAPCAFile)
		}
		if ldap.AllowPasswordsInClear {
			args = append(args, "-ldap_passwords_in_clear_ok=true")
		}
	}
	return args
}

// securityVolumes mounts the Secrets and ConfigMaps referenced by the security spec.
func securityVolumes(c *impalav1alpha1.ImpalaCluster, component Component) ([]corev1.Volume, []corev1.VolumeMount) {
	sec := &c.Spec.ClusterConfig.Security
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	mode := new(int32(0o400))

	if tls := &sec.TLS; TLSEnabled(c) {
		volumes = append(volumes, corev1.Volume{Name: volumeTLS, VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: tls.CertSecretRef.Name, DefaultMode: mode},
		}})
		mounts = append(mounts, corev1.VolumeMount{Name: volumeTLS, MountPath: SecretsDir + "/tls", ReadOnly: true})
	}

	if krb := &sec.Kerberos; KerberosEnabled(c) {
		sources := []corev1.VolumeProjection{{
			Secret: &corev1.SecretProjection{LocalObjectReference: *krb.KeytabSecretRef},
		}}
		if krb.Krb5ConfigMapRef != nil {
			sources = append(sources, corev1.VolumeProjection{
				ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: *krb.Krb5ConfigMapRef},
			})
		}
		volumes = append(volumes, corev1.Volume{Name: volumeKerberos, VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{Sources: sources, DefaultMode: mode},
		}})
		mounts = append(mounts, corev1.VolumeMount{Name: volumeKerberos, MountPath: SecretsDir + "/kerberos", ReadOnly: true})
	}

	if ldap := &sec.LDAP; ldap.Enabled && ldap.CACertSecretRef != nil && component == ComponentCoordinator {
		volumes = append(volumes, corev1.Volume{Name: volumeLDAP, VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: ldap.CACertSecretRef.Name, DefaultMode: mode},
		}})
		mounts = append(mounts, corev1.VolumeMount{Name: volumeLDAP, MountPath: SecretsDir + "/ldap", ReadOnly: true})
	}
	return volumes, mounts
}

// ReferencedSecrets lists Secret names whose rotation should roll pods.
func ReferencedSecrets(c *impalav1alpha1.ImpalaCluster) []string {
	var names []string
	sec := &c.Spec.ClusterConfig.Security
	if sec.TLS.CertSecretRef != nil {
		names = append(names, sec.TLS.CertSecretRef.Name)
	}
	if sec.Kerberos.KeytabSecretRef != nil {
		names = append(names, sec.Kerberos.KeytabSecretRef.Name)
	}
	if sec.LDAP.CACertSecretRef != nil {
		names = append(names, sec.LDAP.CACertSecretRef.Name)
	}
	if s3 := c.Spec.ClusterConfig.Storage.S3; s3 != nil && s3.CredentialsSecretRef != nil {
		names = append(names, s3.CredentialsSecretRef.Name)
	}
	for i := range c.Spec.ClusterConfig.IcebergRESTCatalogs {
		if oauth := c.Spec.ClusterConfig.IcebergRESTCatalogs[i].OAuth2; oauth != nil {
			names = append(names, oauth.CredentialSecretRef.Name)
		}
	}
	return names
}

// ReferencedConfigMaps lists user ConfigMap names whose changes should roll pods.
func ReferencedConfigMaps(c *impalav1alpha1.ImpalaCluster) []string {
	var names []string
	if ref := c.Spec.ClusterConfig.Security.Kerberos.Krb5ConfigMapRef; ref != nil {
		names = append(names, ref.Name)
	}
	return names
}
