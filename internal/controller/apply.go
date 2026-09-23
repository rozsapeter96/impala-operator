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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/rozsapeter96/impala-operator/internal/resources"
)

// applyOwned server-side-applies obj with the cluster as controller owner and
// writes the live object back into obj.
func applyOwned(ctx context.Context, c client.Client, owner client.Object, obj client.Object) error {
	if err := controllerutil.SetControllerReference(owner, obj, c.Scheme()); err != nil {
		return fmt.Errorf("set owner reference on %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	gvk, err := apiutilGVK(c, obj)
	if err != nil {
		return err
	}
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return fmt.Errorf("convert %s %s to unstructured: %w", gvk.Kind, obj.GetName(), err)
	}
	pruneForApply(raw)
	u := &unstructured.Unstructured{Object: raw}
	u.SetGroupVersionKind(gvk)

	if err := c.Apply(ctx, client.ApplyConfigurationFromUnstructured(u),
		client.FieldOwner(resources.FieldManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("apply %s %s/%s: %w", gvk.Kind, obj.GetNamespace(), obj.GetName(), err)
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj); err != nil {
		return fmt.Errorf("decode applied %s %s: %w", gvk.Kind, obj.GetName(), err)
	}
	return nil
}

func apiutilGVK(c client.Client, obj client.Object) (gvk schema.GroupVersionKind, err error) {
	gvks, _, err := c.Scheme().ObjectKinds(obj)
	if err != nil {
		return gvk, fmt.Errorf("resolve kind of %T: %w", obj, err)
	}
	return gvks[0], nil
}

// pruneForApply strips fields that a typed object serialises with zero values
// but that must not be part of an apply configuration: null timestamps and
// the status of the object and of embedded PVC templates.
func pruneForApply(obj map[string]any) {
	delete(obj, "status")
	pruneNulls(obj)
	if spec, ok := obj["spec"].(map[string]any); ok {
		if tmpls, ok := spec["volumeClaimTemplates"].([]any); ok {
			for _, t := range tmpls {
				if m, ok := t.(map[string]any); ok {
					delete(m, "status")
				}
			}
		}
	}
}

func pruneNulls(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if val == nil {
				delete(t, k)
				continue
			}
			if k == "creationTimestamp" {
				delete(t, k)
				continue
			}
			pruneNulls(val)
		}
	case []any:
		for _, e := range t {
			pruneNulls(e)
		}
	}
}
