// Copyright 2019-2021 The Liqo Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package getters

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8s "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	netv1alpha1 "github.com/liqotech/liqo/apis/net/v1alpha1"
)

// GetIPAMStorageByLabel it returns a IPAMStorage instance that matches the given label selector.
// todo: introduce namespace parameter.
func GetIPAMStorageByLabel(ctx context.Context, cl client.Client, lSelector labels.Selector) (*netv1alpha1.IpamStorage, error) {
	list := unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   netv1alpha1.GroupVersion.Group,
		Version: netv1alpha1.GroupVersion.Version,
		Kind:    "IpamStorage",
	})
	obj, err := GetObjByLabel(ctx, cl, &list, "default", lSelector)
	if err != nil {
		return nil, err
	}
	ipam := new(netv1alpha1.IpamStorage)
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.UnstructuredContent(), &ipam)
	if err != nil {
		return nil, err
	}
	return ipam, nil
	/*list := new(netv1alpha1.IpamStorageList)
	if err := cl.List(ctx, list, &client.ListOptions{LabelSelector: lSelector}); err != nil {
		return nil, err
	}

	if len(list.Items) != 1 {
		if len(list.Items) != 0 {
			return nil, fmt.Errorf("multiple resources of type %s found, when only one was expected", netv1alpha1.IpamGroupResource)
		}
		return nil, fmt.Errorf("no ipamstorage found for the given selector %s", lSelector.String())
	}

	return &list.Items[0], nil*/
}

// GetNetworkConfigByLabel it returns a networkconfigs instance that matches the given label selector.
func GetNetworkConfigByLabel(ctx context.Context, cl client.Client, ns string, lSelector labels.Selector) (*netv1alpha1.NetworkConfig, error) {
	list := new(netv1alpha1.NetworkConfigList)
	if err := cl.List(ctx, list, &client.ListOptions{LabelSelector: lSelector}, client.InNamespace(ns)); err != nil {
		return nil, err
	}

	switch len(list.Items) {
	case 0:
		return nil, kerrors.NewNotFound(netv1alpha1.NetworkConfigGroupResource,
			fmt.Sprintf("Remote NetworkConfig for selector: %v", lSelector.String()))
	case 1:
		return &list.Items[0], nil
	default:
		return nil, fmt.Errorf("multiple resources of type %s found, when only one was expected",
			netv1alpha1.NetworkConfigGroupResource)
	}
}

// GetNetworkConfigByLabel it returns a networkconfigs instance that matches the given label selector.
func GetObjByLabel(ctx context.Context, cl client.Client, obj client.ObjectList, ns string, lSelector labels.Selector) (*unstructured.Unstructured, error) {
	if err := cl.List(ctx, obj, &client.ListOptions{LabelSelector: lSelector}, client.InNamespace(ns)); err != nil {
		return nil, err
	}

	u, ok := obj.(*unstructured.UnstructuredList)
	if !ok {
		return nil, fmt.Errorf("unstructured client did not understand object: %T", obj)
	}

	switch len(u.Items) {
	case 0:
		return nil, fmt.Errorf("no networkconfigs found for the given selector %s", lSelector.String())
	case 1:
		return &u.Items[0], nil
	default:
		return nil, fmt.Errorf("multiple resources of type %s found, when only one was expected",
			netv1alpha1.NetworkConfigGroupResource)
	}
}

// GetServiceByLabel it returns a service instance that matches the given label selector.
func GetServiceByLabel(ctx context.Context, cl k8s.Interface, ns string, lSelector *metav1.LabelSelector) (*corev1.Service, error) {
	list, err := cl.CoreV1().Services(ns).List(ctx, metav1.ListOptions{LabelSelector: metav1.FormatLabelSelector(lSelector)})
	if err != nil {
		return nil, err
	}

	if len(list.Items) != 1 {
		if len(list.Items) != 0 {
			return nil, fmt.Errorf("multiple resources of type services found in namespace %s, when only one was expected", ns)
		}
		return nil, fmt.Errorf("no service found for the given selector %s in namespace %s", metav1.FormatLabelSelector(lSelector), ns)
	}

	return &list.Items[0], nil
}

// GetSecretByLabel it returns a secret instance that matches the given label selector.
func GetSecretByLabel(ctx context.Context, cl k8s.Interface, ns string, lSelector *metav1.LabelSelector) (*corev1.Secret, error) {
	list, err := cl.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{LabelSelector: metav1.FormatLabelSelector(lSelector)})
	if err != nil {
		return nil, err
	}

	if len(list.Items) != 1 {
		if len(list.Items) != 0 {
			return nil, fmt.Errorf("multiple resources of type secrets found in namespace %s, when only one was expected", ns)
		}
		return nil, fmt.Errorf("no secret found for the given selector %s in namespace %s", metav1.FormatLabelSelector(lSelector), ns)
	}

	return &list.Items[0], nil
}

// GetConfigMapByLabel it returns a configmap instance that matches the given label selector.
func GetConfigMapByLabel(ctx context.Context, cl k8s.Interface, ns string, lSelector *metav1.LabelSelector) (*corev1.ConfigMap, error) {
	list, err := cl.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{LabelSelector: metav1.FormatLabelSelector(lSelector)})
	if err != nil {
		return nil, err
	}

	if len(list.Items) != 1 {
		if len(list.Items) != 0 {
			return nil, fmt.Errorf("multiple resources of type configmaps found in namespace %s, when only one was expected", ns)
		}
		return nil, fmt.Errorf("no configmap found for the given selector %s in namespace %s", metav1.FormatLabelSelector(lSelector), ns)
	}

	return &list.Items[0], nil
}
