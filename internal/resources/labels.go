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

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

const (
	LabelName      = "app.kubernetes.io/name"
	LabelInstance  = "app.kubernetes.io/instance"
	LabelComponent = "app.kubernetes.io/component"
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelVersion   = "app.kubernetes.io/version"

	// LabelExecutorGroup carries the executor group family name.
	LabelExecutorGroup = "impala.operator.dev/executor-group"
	// LabelExecutorGroupIndex carries the executor group instance index.
	LabelExecutorGroupIndex = "impala.operator.dev/executor-group-index"

	// AnnotationConfigHash triggers a rolling restart when generated config changes.
	AnnotationConfigHash = "impala.operator.dev/config-hash"

	AppName      = "impala"
	ManagedBy    = "impala-operator"
	FieldManager = "impala-operator"
)

// SelectorLabels are the immutable labels used in selectors.
func SelectorLabels(c *impalav1alpha1.ImpalaCluster, component Component) map[string]string {
	return map[string]string{
		LabelName:      AppName,
		LabelInstance:  c.Name,
		LabelComponent: string(component),
	}
}

// Labels are the full labels applied to generated objects.
func Labels(c *impalav1alpha1.ImpalaCluster, component Component) map[string]string {
	l := SelectorLabels(c, component)
	l[LabelManagedBy] = ManagedBy
	if v := c.Spec.Image.Version; v != "" {
		l[LabelVersion] = v
	}
	return l
}

// ExecutorGroupSelectorLabels selects pods of one executor group instance.
func ExecutorGroupSelectorLabels(c *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec, idx int32) map[string]string {
	l := SelectorLabels(c, ComponentExecutor)
	l[LabelExecutorGroup] = eg.Name
	l[LabelExecutorGroupIndex] = itoa(idx)
	return l
}

// ExecutorGroupLabels are the full labels for one executor group instance.
func ExecutorGroupLabels(c *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec, idx int32) map[string]string {
	l := Labels(c, ComponentExecutor)
	l[LabelExecutorGroup] = eg.Name
	l[LabelExecutorGroupIndex] = itoa(idx)
	return l
}

func merge(dst map[string]string, srcs ...map[string]string) map[string]string {
	if dst == nil {
		dst = map[string]string{}
	}
	for _, s := range srcs {
		maps.Copy(dst, s)
	}
	return dst
}

func itoa(i int32) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [12]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}
