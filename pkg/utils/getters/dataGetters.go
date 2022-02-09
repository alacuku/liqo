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
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	discoveryv1alpha1 "github.com/liqotech/liqo/apis/discovery/v1alpha1"
	liqoconsts "github.com/liqotech/liqo/pkg/consts"
)

// GetClusterIDFromConfigMap retrieves ClusterIdentity from a given configmap.
func GetClusterIDFromConfigMap(cm *corev1.ConfigMap) (*discoveryv1alpha1.ClusterIdentity, error) {
	id, found := cm.Data[liqoconsts.ClusterIDConfigMapKey]
	if !found {
		return nil, fmt.Errorf("unable to get cluster ID: field {%s} not found in configmap {%s/%s}",
			liqoconsts.ClusterIDConfigMapKey, cm.Namespace, cm.Name)
	}

	name, found := cm.Data[liqoconsts.ClusterNameConfigMapKey]
	if !found {
		return nil, fmt.Errorf("unable to get cluster name: field {%s} not found in configmap {%s/%s}",
			liqoconsts.ClusterNameConfigMapKey, cm.Namespace, cm.Name)
	}

	return &discoveryv1alpha1.ClusterIdentity{
		ClusterID:   id,
		ClusterName: name,
	}, nil
}

// GetAuthEndpointFromService the auth endpoint from a service.
func GetAuthEndpointFromService(svc *corev1.Service, portName string) (endpointIP, endpointPort string, err error) {
	endpointIP = svc.Spec.ClusterIP
	// TODO: move the code that extracts the port in a separate function.
	for _, port := range svc.Spec.Ports {
		if port.Name == portName {
			endpointPort = strconv.FormatInt(int64(port.Port), 10)
			return endpointIP, endpointPort, nil
		}
	}

	err = fmt.Errorf("port %s not found in service %q", portName, klog.KObj(svc))
	return endpointIP, endpointPort, err
}
