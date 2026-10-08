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
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

const (
	volumeCatalogs = "catalogs"

	// restCatalogKeyPrefix prefixes the ConfigMap keys holding REST catalog
	// properties files, so they can be projected into their own directory.
	restCatalogKeyPrefix = "rest-catalog-"

	// restCatalogEnvPrefix prefixes the environment variable carrying a
	// catalog's OAuth2 client credential.
	restCatalogEnvPrefix = "IMPALA_REST_CATALOG_"

	// DefaultOAuth2CredentialKey is the Secret key read when
	// oauth2.credentialSecretRef.key is unset.
	DefaultOAuth2CredentialKey = "credential"
)

// RESTCatalogFileKey is the ConfigMap key of a catalog's properties file.
func RESTCatalogFileKey(cat *impalav1alpha1.IcebergRESTCatalogSpec) string {
	return restCatalogKeyPrefix + cat.Name + ".properties"
}

// RESTCatalogCredentialEnv is the environment variable that carries the
// catalog's OAuth2 credential. Impala's ${ENV:...} substitution accepts
// [a-zA-Z][a-zA-Z0-9_-]*, which the upper-cased catalog name satisfies.
func RESTCatalogCredentialEnv(cat *impalav1alpha1.IcebergRESTCatalogSpec) string {
	return restCatalogEnvPrefix + strings.ToUpper(strings.ReplaceAll(cat.Name, "-", "_")) + "_CREDENTIAL"
}

// restCatalogProperties renders the properties Impala reads for one catalog.
// It uses the Trino-compatible key names Impala documents; unknown keys from
// spec.properties pass through to the Iceberg RESTCatalog unchanged.
func restCatalogProperties(cat *impalav1alpha1.IcebergRESTCatalogSpec) map[string]string {
	props := map[string]string{
		// Both are mandatory markers checked by Impala's ConfigLoader.
		"connector.name":            "iceberg",
		"iceberg.catalog.type":      "rest",
		"iceberg.rest-catalog.name": cat.Name,
		"iceberg.rest-catalog.uri":  cat.URI,
	}
	if cat.Warehouse != "" {
		props["iceberg.rest-catalog.warehouse"] = cat.Warehouse
	}
	if cat.Prefix != "" {
		props["iceberg.rest-catalog.prefix"] = cat.Prefix
	}
	if cat.VendedCredentials {
		props["iceberg.rest-catalog.vended-credentials-enabled"] = trueStr
	}
	if oauth := cat.OAuth2; oauth != nil {
		props["iceberg.rest-catalog.security"] = "OAUTH2"
		// The secret value is resolved by Impala at startup from the pod
		// environment, so the ConfigMap only carries the reference.
		props["iceberg.rest-catalog.oauth2.credential"] = "${ENV:" + RESTCatalogCredentialEnv(cat) + "}"
		// Iceberg's fallback to "<uri>/v1/oauth/tokens" is deprecated and
		// logs a warning, so the default is spelled out.
		serverURI := oauth.ServerURI
		if serverURI == "" {
			serverURI = strings.TrimSuffix(cat.URI, "/") + "/v1/oauth/tokens"
		}
		props["iceberg.rest-catalog.oauth2.server-uri"] = serverURI
		if oauth.Scope != "" {
			props["iceberg.rest-catalog.oauth2.scope"] = oauth.Scope
		}
	}
	maps.Copy(props, cat.Properties)
	return props
}

// restCatalogEnv returns the Secret-backed environment variables the
// coordinators need to resolve the ${ENV:...} references in the properties.
func restCatalogEnv(c *impalav1alpha1.ImpalaCluster) []corev1.EnvVar {
	var env []corev1.EnvVar
	for i := range c.Spec.ClusterConfig.IcebergRESTCatalogs {
		cat := &c.Spec.ClusterConfig.IcebergRESTCatalogs[i]
		if cat.OAuth2 == nil {
			continue
		}
		key := cat.OAuth2.CredentialSecretRef.Key
		if key == "" {
			key = DefaultOAuth2CredentialKey
		}
		env = append(env, corev1.EnvVar{
			Name: RESTCatalogCredentialEnv(cat),
			ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: cat.OAuth2.CredentialSecretRef.Name},
				Key:                  key,
			}},
		})
	}
	return env
}

// restCatalogVolumes projects the catalog properties files out of the shared
// ConfigMap into their own directory. Impala loads every file it finds under
// -catalog_config_dir, so the Hadoop XML files must not be visible there.
func restCatalogVolumes(c *impalav1alpha1.ImpalaCluster) []corev1.Volume {
	cats := c.Spec.ClusterConfig.IcebergRESTCatalogs
	if len(cats) == 0 {
		return nil
	}
	items := make([]corev1.KeyToPath, 0, len(cats))
	for i := range cats {
		items = append(items, corev1.KeyToPath{Key: RESTCatalogFileKey(&cats[i]), Path: cats[i].Name + ".properties"})
	}
	return []corev1.Volume{{
		Name: volumeCatalogs,
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: ConfigMapName(c)},
			Items:                items,
		}},
	}}
}

func restCatalogMounts(c *impalav1alpha1.ImpalaCluster) []corev1.VolumeMount {
	if len(c.Spec.ClusterConfig.IcebergRESTCatalogs) == 0 {
		return nil
	}
	return []corev1.VolumeMount{{Name: volumeCatalogs, MountPath: CatalogConfigDir, ReadOnly: true}}
}

// javaProperties renders a map in java.util.Properties format with sorted
// keys, escaping the characters the loader treats specially.
func javaProperties(props map[string]string) string {
	keys := slices.Sorted(maps.Keys(props))
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(propertiesEscape(k, true))
		b.WriteByte('=')
		b.WriteString(propertiesEscape(props[k], false))
		b.WriteByte('\n')
	}
	return b.String()
}

// propertiesEscape escapes a key or value for java.util.Properties. Keys
// additionally escape the separators; values only need a leading space
// protected, since the loader trims it.
func propertiesEscape(s string, key bool) string {
	var b strings.Builder
	for i, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\f':
			b.WriteString(`\f`)
		case '=', ':', '#', '!':
			if key {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		case ' ':
			if key || i == 0 {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
