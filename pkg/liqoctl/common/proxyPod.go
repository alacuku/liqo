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

package common

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8s "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	proxyLabelKey   = "run"
	proxyLabelValue = "api-proxy"
	proxyImage      = "envoyproxy/envoy:v1.21.0"
)

var (
	proxyConfig = `
admin:
  address:
    socket_address:
      protocol: TCP
      address: 0.0.0.0
      port_value: 9901

static_resources:
  listeners:
  - name: listener_http
    address:
      socket_address:
        protocol: TCP
        address: 0.0.0.0
        port_value: 8118
    access_log:
      name: envoy.access_loggers.file
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
        path: /dev/stdout
    filter_chains:
    - filters:
      - name: envoy.filters.network.http_connection_manager
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
          stat_prefix: ingress_http
          route_config:
            name: local_route
            virtual_hosts:
            - name: local_service
              domains:
              - "*"
              routes:
              - match:
                  connect_matcher:
                    {}
                route:
                  cluster: api_server
                  upgrade_configs:
                  - upgrade_type: CONNECT
                    connect_config:
                      {}
          http_filters:
          - name: envoy.filters.http.router
  clusters:
  - name: api_server
    connect_timeout: 0.25s
    type: STRICT_DNS
    respect_dns_ttl: true
    dns_lookup_family: V4_ONLY
    dns_refresh_rate: 300s
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: api_server
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
               address: kubernetes.default
               port_value: 443`
)

type Endpoint struct {
	ip         string
	port       string
	remappedIP string
}

func (ep *Endpoint) GetIP() string {
	return ep.ip
}

func (ep *Endpoint) SetRemappedIP(ip string) {
	ep.remappedIP = ip
}

func (ep *Endpoint) GetHTTPURL() string {
	return fmt.Sprintf("http://%s:%s", ep.remappedIP, ep.port)
}

func (ep *Endpoint) GetHTTPSURL() string {
	return fmt.Sprintf("https://%s:%s", ep.remappedIP, ep.port)
}

func createProxy(ctx context.Context, cl k8s.Interface, podName, ns string) (*Endpoint, error) {

	commonObjMeta := metav1.ObjectMeta{
		Name:      podName,
		Namespace: ns,
		Labels: map[string]string{
			proxyLabelKey: proxyLabelValue,
		},
	}

	proxyConfigMap := &corev1.ConfigMap{
		ObjectMeta: commonObjMeta,
		Data: map[string]string{
			"config": proxyConfig,
		},
	}

	proxyService := &corev1.Service{
		ObjectMeta: commonObjMeta,
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name:        "http",
				Protocol:    "TCP",
				AppProtocol: nil,
				Port:        8118,
				TargetPort: intstr.IntOrString{
					Type:   intstr.Int,
					IntVal: 8118,
					StrVal: "8118",
				},
			}},
			Selector: map[string]string{
				proxyLabelKey: proxyLabelValue,
			},
		},
	}

	proxyPod := &corev1.Pod{
		ObjectMeta: commonObjMeta,
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "envoy",
					Image: proxyImage,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "config-volume",
							MountPath: "/etc/envoy/envoy.yaml",
							SubPath:   "config",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "config-volume",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: proxyName,
							},
						},
					},
				},
			},
		},
	}
	_, err := cl.CoreV1().Pods(ns).Create(ctx, proxyPod, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}

	_, err = cl.CoreV1().ConfigMaps(ns).Create(ctx, proxyConfigMap, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}

	svc, err := cl.CoreV1().Services(ns).Create(ctx, proxyService, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}
	if k8serrors.IsAlreadyExists(err) {
		svc, err = cl.CoreV1().Services(ns).Get(ctx, proxyPod.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
	}

	return &Endpoint{
		ip:   svc.Spec.ClusterIP,
		port: strconv.FormatInt(int64(svc.Spec.Ports[0].Port), 10),
	}, err
}

func deleteProxy(ctx context.Context, cl k8s.Interface, podName, ns string) error {
	// Remove proxy service.
	if err := cl.CoreV1().Services(ns).Delete(ctx, podName, metav1.DeleteOptions{}); client.IgnoreNotFound(err) != nil {
		return err
	}

	// Remove proxy configmap.
	if err := cl.CoreV1().ConfigMaps(ns).Delete(ctx, podName, metav1.DeleteOptions{}); client.IgnoreNotFound(err) != nil {
		return err
	}

	// Remove proxy pod.
	return client.IgnoreNotFound(cl.CoreV1().Pods(ns).Delete(ctx, podName, metav1.DeleteOptions{}))
}
