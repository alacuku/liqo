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

package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	discoveryv1alpha1 "github.com/liqotech/liqo/apis/discovery/v1alpha1"
	"github.com/liqotech/liqo/internal/liqonet/network-manager/httpserver"
	"github.com/liqotech/liqo/pkg/auth"
	liqoconst "github.com/liqotech/liqo/pkg/consts"
	"github.com/liqotech/liqo/pkg/discovery"
	"github.com/liqotech/liqo/pkg/utils/authenticationtoken"
	foreigncluster "github.com/liqotech/liqo/pkg/utils/foreignCluster"
)

var (
	// This labels are the ones set during the deployment of liqo using the helm chart.
	// Any change to those labels on the helm chart has also to be reflected here.
	podLabels = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app.kubernetes.io/instance": "liqo-network-manager",
			"app.kubernetes.io/name":     "network-manager"},
	}
	remotePort = 8080

	svcLabels = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app.kubernetes.io/instance": "liqo-auth",
			"app.kubernetes.io/name":     "auth"},
	}
)

type cluster struct {
	client     k8s.Interface
	restConfig *rest.Config
	namespace  string
	netConfig  httpserver.NetworkConfiguration
	stopChan   chan struct{}
	remotePort int
	localPort  int
	proxyIP    string
	proxyPort  int32
	authIP     string
	authPort   int32
	token      string
}

// NewCluster returns a new cluster object. The cluster has to be initialized before being consumed.
func NewCluster(kubeconfig, namespace string) (*cluster, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	client, err := k8s.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &cluster{
		client:     client,
		namespace:  namespace,
		remotePort: remotePort,
		restConfig: config,
	}, nil
}

func (c *cluster) Init() error {
	ctx := context.Background()
	c.stopChan = make(chan struct{})
	readyChan := make(chan struct{})
	errChan := make(chan error, 1)

	// Get the local port used to forward the pod's port.
	localPort, err := getFreePort()
	if err != nil {
		return fmt.Errorf("unable to get a local port: %w", err)
	}

	c.localPort = localPort

	podURL, err := c.getPodURL(ctx)
	if err != nil {
		return err
	}

	dialer, err := c.newDialer(podURL)
	if err != nil {
		return fmt.Errorf("unable to create dialer: %w", err)
	}

	ports := []string{
		fmt.Sprintf("%d:%d", localPort, c.remotePort),
	}

	discard := io.Discard
	pf, err := portforward.New(dialer, ports, c.stopChan, readyChan, discard, discard)
	if err != nil {
		return fmt.Errorf("unable to port forward into pod %s: %w", podURL.String(), err)
	}

	go func() {
		errChan <- pf.ForwardPorts()
	}()

	select {
	case err = <-errChan:
		if err != nil {
			return fmt.Errorf("an error occurred while port forwarding: %w", err)
		}
	case <-readyChan:
		break
	}

	fmt.Println(fmt.Sprintf("port forwarding local port %d -> to remote pods port %d", c.localPort, c.remotePort))

	return c.getConfig()
}

// Stop stops the port forwarding.
func (c *cluster) Stop() {
	c.stopChan <- struct{}{}
}

func (c *cluster) getPodURL(ctx context.Context) (*url.URL, error) {
	pods, err := c.client.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&podLabels),
		FieldSelector: fields.OneTermEqualSelector("status.phase", string(v1.PodRunning)).String(),
	})

	if err != nil {
		return nil, fmt.Errorf("unable to list pods: %w", err)
	}

	labels := metav1.FormatLabelSelector(&podLabels)

	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no running pods found for selector: %s", labels)
	}

	if len(pods.Items) != 1 {
		return nil, fmt.Errorf("multiple pods found for selector: %s", labels)
	}

	return c.client.CoreV1().RESTClient().Post().Resource("pods").Namespace(pods.Items[0].Namespace).Name(pods.Items[0].Name).SubResource("portforward").URL(), nil
}

func (c *cluster) newDialer(podURL *url.URL) (httpstream.Dialer, error) {
	rt, upgrader, err := spdy.RoundTripperFor(c.restConfig)
	if err != nil {
		return nil, err
	}

	return spdy.NewDialer(upgrader, &http.Client{Transport: rt}, http.MethodPost, podURL), nil
}

func (c *cluster) getConfig() error {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/config", c.localPort))
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return err
	}
	if err := json.NewDecoder(strings.NewReader(string(body))).Decode(&c.netConfig); err != nil {
		return err
	}

	return nil
}

func (c *cluster) mapCluster(config httpserver.NetworkConfiguration) (*httpserver.ClusterMappingReq, error) {
	url := fmt.Sprintf("http://localhost:%d/clusters", c.localPort)
	clusterConfig := &httpserver.ClusterMappingReq{
		ClusterID:    config.ClusterID,
		PodCIDR:      config.PodCIDR,
		ExternalCIDR: config.ExternalCIDR,
	}
	jsonData, err := json.Marshal(clusterConfig)
	if err != nil {
		return nil, err
	}
	// First we map the cluster.
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if err := json.NewDecoder(strings.NewReader(string(body))).Decode(&clusterConfig); err != nil {
		return nil, err
	}
	// Set the default values in case the CIDRs have not been remapped
	if clusterConfig.PodCIDR == clusterConfig.PodCIDRNAT {
		clusterConfig.PodCIDRNAT = liqoconst.DefaultCIDRValue
	}
	if clusterConfig.ExternalCIDR == clusterConfig.ExternalCIDRNAT {
		clusterConfig.ExternalCIDRNAT = liqoconst.DefaultCIDRValue
	}
	return clusterConfig, nil
}

func (c *cluster) CreateTEP(config *httpserver.NetworkConfiguration, remoteMapping, localMapping *httpserver.ClusterMappingReq) error {
	url := fmt.Sprintf("http://localhost:%d/tep", c.localPort)

	tepSpec := &httpserver.TunnelEndpointSpec{
		ClusterID:             config.ClusterID,
		LocalPodCIDR:          localMapping.PodCIDR,
		LocalNATPodCIDR:       localMapping.PodCIDRNAT,
		LocalExternalCIDR:     localMapping.ExternalCIDR,
		LocalNATExternalCIDR:  localMapping.ExternalCIDRNAT,
		RemotePodCIDR:         remoteMapping.PodCIDR,
		RemoteNATPodCIDR:      remoteMapping.PodCIDRNAT,
		RemoteExternalCIDR:    remoteMapping.ExternalCIDR,
		RemoteNATExternalCIDR: remoteMapping.ExternalCIDRNAT,
		VPNBackend:            config.VPNBackend,
		EndpointIP:            config.EndpointIP,
		BackendConfig:         config.BackendConfig,
	}

	jsonData, err := json.Marshal(tepSpec)
	if err != nil {
		return err
	}
	// First we map the cluster.
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonData))
	req.Close = true
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	fmt.Println(resp.StatusCode)
	return nil
}

func (c *cluster) MapIP(remoteClusterID, IPAddress string) (*httpserver.MapResponse, error) {
	url := fmt.Sprintf("http://localhost:%d/ip", c.localPort)
	remoteMapping := &httpserver.MapRequest{
		ClusterID: remoteClusterID,
		Ip:        IPAddress,
	}

	jsonData, err := json.Marshal(remoteMapping)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonData))
	req.Close = true
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	client := &http.Client{}
	fmt.Printf("%s -> request to map ip address %s living in cluster %s \n", c.netConfig.ClusterID, IPAddress, remoteClusterID)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("an error occurred with code: %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	mapResp := new(httpserver.MapResponse)
	if err := json.NewDecoder(strings.NewReader(string(body))).Decode(mapResp); err != nil {
		return nil, err
	}
	fmt.Printf("%s -> ip address %s living in cluster %s successfully mapped by local cluster to %s\n", c.netConfig.ClusterID, IPAddress, remoteClusterID, mapResp.Ip)
	return mapResp, nil
}

func (c *cluster) setUpProxy(ctx context.Context) error {

	proxyService := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "liqo-proxy",
			Namespace: c.namespace,
			Labels: map[string]string{
				"run": "api-proxy",
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
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
				"run": "api-proxy",
			},
		},
	}

	liqoProxyPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "liqo-proxy",
			Namespace: c.namespace,
			Labels:    map[string]string{"run": "api-proxy"},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "privoxy",
					Image: "aldokcl/dronet:privoxy7",
				},
			},
		},
	}
	_, err := c.client.CoreV1().Pods(c.namespace).Create(ctx, liqoProxyPod, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}

	svc, err := c.client.CoreV1().Services(c.namespace).Create(ctx, proxyService, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	if k8serrors.IsAlreadyExists(err) {
		svc, err = c.client.CoreV1().Services(c.namespace).Get(ctx, liqoProxyPod.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
	}

	c.proxyIP = svc.Spec.ClusterIP
	c.proxyPort = svc.Spec.Ports[0].Port

	return nil
}

func (c *cluster) getAuthIP(ctx context.Context) error {
	svcs, err := c.client.CoreV1().Services(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&svcLabels),
	})

	if err != nil {
		return fmt.Errorf("unable to list svcs: %w", err)
	}

	labels := metav1.FormatLabelSelector(&podLabels)

	if len(svcs.Items) == 0 {
		return fmt.Errorf("no running svcs found for selector: %s", labels)
	}

	if len(svcs.Items) != 1 {
		return fmt.Errorf("multiple svcs found for selector: %s", labels)
	}
	c.authIP = svcs.Items[0].Spec.ClusterIP
	c.authPort = svcs.Items[0].Spec.Ports[0].Port

	return nil
}

func (c *cluster) getToken(ctx context.Context) error {
	clientSet, err := client.New(c.restConfig, client.Options{})
	if err != nil {
		return err
	}
	c.token, err = auth.GetToken(ctx, clientSet, c.namespace)
	if err != nil {
		fmt.Printf("%s -> unable to get token: %s", err)
		return err
	}
	return nil
}

func (c *cluster) addCluster(ctx context.Context, name, id, token, authURL, proxyURL string) error {

	if c.netConfig.ClusterID == id {
		return fmt.Errorf("the cluster ID of the cluster to be added is equal to the local cluster")
	}

	clientSet, err := client.New(c.restConfig, client.Options{})
	if err != nil {
		return err
	}

	if err := authenticationtoken.StoreInSecret(ctx, c.client, id, token, c.namespace); err != nil {
		return fmt.Errorf("%s -> unable to add cluster %s: %w", c.netConfig.ClusterID, id, err)
	}

	// Create ForeignCluster
	fc, err := foreigncluster.GetForeignClusterByID(ctx, clientSet, id)
	if k8serrors.IsNotFound(err) {
		fc = &discoveryv1alpha1.ForeignCluster{ObjectMeta: metav1.ObjectMeta{Name: name,
			Labels: map[string]string{discovery.ClusterIDLabel: id}}}
	} else if err != nil {
		return err
	}

	_, err = controllerutil.CreateOrUpdate(ctx, clientSet, fc, func() error {
		fc.Spec.ForeignAuthURL = authURL
		fc.Spec.ForeignProxyURL = proxyURL
		fc.Spec.OutgoingPeeringEnabled = discoveryv1alpha1.PeeringEnabledYes
		if fc.Spec.IncomingPeeringEnabled == "" {
			fc.Spec.IncomingPeeringEnabled = discoveryv1alpha1.PeeringEnabledAuto
		}
		if fc.Spec.InsecureSkipTLSVerify == nil {
			fc.Spec.InsecureSkipTLSVerify = pointer.BoolPtr(true)
		}
		return nil
	})
	return err
}
