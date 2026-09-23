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
	"encoding/xml"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

const (
	trueStr  = "true"
	falseStr = "false"
)

// File names inside the config ConfigMap.
const (
	FileHiveSite      = "hive-site.xml"
	FileCoreSite      = "core-site.xml"
	FileHdfsSite      = "hdfs-site.xml"
	FileFairScheduler = "fair-scheduler.xml"
	FileLlamaSite     = "llama-site.xml"
)

// BuildConfigMap renders every Hadoop-style config file Impala needs.
func BuildConfigMap(c *impalav1alpha1.ImpalaCluster) *corev1.ConfigMap {
	cc := &c.Spec.ClusterConfig
	data := map[string]string{
		FileHiveSite: hadoopXML(withOverrides(hiveSiteProps(cc), cc.ConfigOverrides[FileHiveSite])),
		FileCoreSite: hadoopXML(withOverrides(coreSiteProps(cc), cc.ConfigOverrides[FileCoreSite])),
		FileHdfsSite: hadoopXML(withOverrides(map[string]string{}, cc.ConfigOverrides[FileHdfsSite])),
	}
	if HasPools(c) {
		data[FileFairScheduler] = fairSchedulerXML(cc.AdmissionControl.Pools, DefaultPool(c))
		data[FileLlamaSite] = hadoopXML(llamaSiteProps(cc.AdmissionControl.Pools))
	}
	// Any other override file the user names is rendered verbatim as properties.
	for file, props := range cc.ConfigOverrides {
		if _, known := data[file]; !known {
			data[file] = hadoopXML(props)
		}
	}
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName(c),
			Namespace: c.Namespace,
			Labels:    Labels(c, "config"),
		},
		Data: data,
	}
}

// DefaultPool returns the fully qualified pool for queries without REQUEST_POOL.
func DefaultPool(c *impalav1alpha1.ImpalaCluster) string {
	ac := &c.Spec.ClusterConfig.AdmissionControl
	if len(ac.Pools) == 0 {
		return DefaultPoolName
	}
	if ac.DefaultPool != "" {
		return "root." + ac.DefaultPool
	}
	return "root." + ac.Pools[0].Name
}

// HasPools reports whether admission-control pools are configured.
func HasPools(c *impalav1alpha1.ImpalaCluster) bool {
	return len(c.Spec.ClusterConfig.AdmissionControl.Pools) > 0
}

// ConfigHash returns a stable digest of the ConfigMap contents, used to roll
// pods when configuration changes.
func ConfigHash(cm *corev1.ConfigMap) string {
	keys := slices.Sorted(maps.Keys(cm.Data))
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(cm.Data[k]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func hiveSiteProps(cc *impalav1alpha1.ClusterConfig) map[string]string {
	return map[string]string{
		"hive.metastore.uris": cc.HiveMetastore.URIs,
		// Required for catalogd HMS event polling.
		"hive.metastore.dml.events":                     trueStr,
		"hive.metastore.event.db.notification.api.auth": falseStr,
		"hive.metastore.execute.setugi":                 trueStr,
	}
}

func coreSiteProps(cc *impalav1alpha1.ClusterConfig) map[string]string {
	props := map[string]string{}
	if s3 := cc.Storage.S3; s3 != nil {
		props["fs.s3a.impl"] = "org.apache.hadoop.fs.s3a.S3AFileSystem"
		props["fs.s3a.connection.maximum"] = "1500"
		if s3.Endpoint != "" {
			props["fs.s3a.endpoint"] = s3.Endpoint
			if strings.HasPrefix(s3.Endpoint, "http://") {
				props["fs.s3a.connection.ssl.enabled"] = falseStr
			}
		}
		if s3.Region != "" {
			props["fs.s3a.endpoint.region"] = s3.Region
		}
		if s3.PathStyleAccess {
			props["fs.s3a.path.style.access"] = trueStr
		}
		if s3.CredentialsSecretRef != nil {
			// Credentials arrive as AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY env vars.
			props["fs.s3a.aws.credentials.provider"] = "com.amazonaws.auth.EnvironmentVariableCredentialsProvider"
		}
	}
	if cc.Security.Kerberos.Enabled {
		props["hadoop.security.authentication"] = "kerberos"
	}
	return props
}

func llamaSiteProps(pools []impalav1alpha1.PoolSpec) map[string]string {
	props := map[string]string{}
	for i := range pools {
		p := &pools[i]
		name := "root." + p.Name
		if p.MaxRunningQueries != nil {
			props["llama.am.throttling.maximum.placed.reservations."+name] = fmt.Sprint(*p.MaxRunningQueries)
		}
		if p.MaxQueuedQueries != nil {
			props["llama.am.throttling.maximum.queued.reservations."+name] = fmt.Sprint(*p.MaxQueuedQueries)
		}
		if p.QueueTimeoutMs != nil {
			props["impala.admission-control.pool-queue-timeout-ms."+name] = fmt.Sprint(*p.QueueTimeoutMs)
		}
		if p.MaxQueryMemLimit != "" {
			props["impala.admission-control.max-query-mem-limit."+name] = p.MaxQueryMemLimit
		}
		if p.MinQueryMemLimit != "" {
			props["impala.admission-control.min-query-mem-limit."+name] = p.MinQueryMemLimit
		}
		if len(p.DefaultQueryOptions) > 0 {
			props["impala.admission-control.pool-default-query-options."+name] = joinOptions(p.DefaultQueryOptions)
		}
	}
	return props
}

// joinOptions renders a map as "k=v,k=v" with sorted keys.
func joinOptions(m map[string]string) string {
	keys := slices.Sorted(maps.Keys(m))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

func withOverrides(base, overrides map[string]string) map[string]string {
	maps.Copy(base, overrides)
	return base
}

type hadoopConfiguration struct {
	XMLName    xml.Name         `xml:"configuration"`
	Properties []hadoopProperty `xml:"property"`
}

type hadoopProperty struct {
	Name  string `xml:"name"`
	Value string `xml:"value"`
}

// hadoopXML renders properties as a Hadoop configuration document with sorted keys.
func hadoopXML(props map[string]string) string {
	keys := slices.Sorted(maps.Keys(props))
	doc := hadoopConfiguration{}
	for _, k := range keys {
		doc.Properties = append(doc.Properties, hadoopProperty{Name: k, Value: props[k]})
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		// Only unmarshalable types can fail here, and we control the input.
		panic(err)
	}
	return xml.Header + string(out) + "\n"
}

type fairScheduler struct {
	XMLName   xml.Name       `xml:"allocations"`
	Root      fairQueue      `xml:"queue"`
	Placement queuePlacement `xml:"queuePlacementPolicy"`
}

type queuePlacement struct {
	Rules []placementRule `xml:"rule"`
}

type placementRule struct {
	Name   string `xml:"name,attr"`
	Create string `xml:"create,attr,omitempty"`
	Queue  string `xml:"queue,attr,omitempty"`
}

type fairQueue struct {
	Name         string      `xml:"name,attr"`
	MaxResources string      `xml:"maxResources,omitempty"`
	ACLSubmit    string      `xml:"aclSubmitApps,omitempty"`
	Queues       []fairQueue `xml:"queue,omitempty"`
}

func fairSchedulerXML(pools []impalav1alpha1.PoolSpec, defaultPool string) string {
	root := fairQueue{Name: "root"}
	for i := range pools {
		p := &pools[i]
		q := fairQueue{Name: p.Name, ACLSubmit: "*"}
		if p.MaxMemResources != "" {
			q.MaxResources = p.MaxMemResources + ", 0 vcores"
		}
		root.Queues = append(root.Queues, q)
	}
	// Without an explicit policy the fair scheduler creates a "root.<user>"
	// pool per user, which no executor group serves.
	placement := queuePlacement{Rules: []placementRule{
		{Name: "specified", Create: falseStr},
		{Name: "default", Queue: defaultPool},
	}}
	out, err := xml.MarshalIndent(fairScheduler{Root: root, Placement: placement}, "", "  ")
	if err != nil {
		panic(err)
	}
	return xml.Header + string(out) + "\n"
}
