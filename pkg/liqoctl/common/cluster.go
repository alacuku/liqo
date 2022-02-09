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
	"reflect"
	"strconv"
	"time"

	"github.com/pterm/pterm"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	discoveryv1alpha1 "github.com/liqotech/liqo/apis/discovery/v1alpha1"
	netv1alpha1 "github.com/liqotech/liqo/apis/net/v1alpha1"
	sharingv1alpha1 "github.com/liqotech/liqo/apis/sharing/v1alpha1"
	"github.com/liqotech/liqo/pkg/auth"
	liqoconsts "github.com/liqotech/liqo/pkg/consts"
	"github.com/liqotech/liqo/pkg/discovery"
	"github.com/liqotech/liqo/pkg/liqonet/ipam"
	"github.com/liqotech/liqo/pkg/liqonet/tunnel/wireguard"
	liqonetutils "github.com/liqotech/liqo/pkg/liqonet/utils"
	tenantnamespace "github.com/liqotech/liqo/pkg/tenantNamespace"
	"github.com/liqotech/liqo/pkg/utils/authenticationtoken"
	foreigncluster "github.com/liqotech/liqo/pkg/utils/foreignCluster"
	liqogetters "github.com/liqotech/liqo/pkg/utils/getters"
	liqolabels "github.com/liqotech/liqo/pkg/utils/labels"
)

const (
	Cluster1Name  = "cluster1"
	Cluster1Color = pterm.FgLightBlue
	Cluster2Name  = "cluster2"
	Cluster2Color = pterm.FgLightMagenta

	proxyName = "liqo-proxy"
)

var (
	Scheme *runtime.Scheme
)

func init() {
	Scheme = runtime.NewScheme()
	utilruntime.Must(sharingv1alpha1.AddToScheme(Scheme))
	utilruntime.Must(discoveryv1alpha1.AddToScheme(Scheme))
	utilruntime.Must(netv1alpha1.AddToScheme(Scheme))
	utilruntime.Must(corev1.AddToScheme(Scheme))
}

type Cluster struct {
	locK8sClient       k8s.Interface
	locCtrlRunClient   client.Client
	remCtrlRunClient   client.Client
	locTenantNamespace string
	remTenantNamespace string
	namespaceManager   tenantnamespace.Manager
	namespace          string
	name               string
	clusterID          *discoveryv1alpha1.ClusterIdentity
	netConfig          *liqonetutils.NetworkConfig
	wgConfig           *liqonetutils.WireGuardConfig
	printer            Printer
	PortForwardOpts    *PortForwardOptions
	proxyEP            *Endpoint
	authEP             *Endpoint
	authToken          string
}

// NewCluster returns a new cluster object. The cluster has to be initialized before being consumed.
func NewCluster(localK8sClient k8s.Interface, localCtrlRunClient, remoteCtrlRunClient client.Client, restConfig *rest.Config, namespace, name string, printerColor pterm.Color) *Cluster {
	genericPrinter := pterm.PrefixPrinter{
		Prefix: pterm.Prefix{},
		Scope: pterm.Scope{
			Text:  name,
			Style: pterm.NewStyle(printerColor),
		},
		MessageStyle: pterm.NewStyle(pterm.FgDefault),
	}

	successPrinter := SuccessPrinter.WithScope(
		pterm.Scope{
			Text:  name,
			Style: pterm.NewStyle(printerColor),
		})

	warningPrinter := WarningPrinter.WithScope(
		pterm.Scope{
			Text:  name,
			Style: pterm.NewStyle(printerColor),
		})

	errorPrinter := ErrorPrinter.WithScope(
		pterm.Scope{
			Text:  name,
			Style: pterm.NewStyle(printerColor),
		})

	printer := Printer{
		Info: genericPrinter.WithPrefix(pterm.Prefix{
			Text:  "[INFO]",
			Style: pterm.NewStyle(pterm.FgCyan),
		}),
		Success: successPrinter,
		Warning: warningPrinter,
		Error:   errorPrinter,
		Spinner: &pterm.SpinnerPrinter{
			Sequence:            SpinnerCharset,
			Style:               pterm.NewStyle(printerColor),
			Delay:               time.Millisecond * 100,
			MessageStyle:        pterm.NewStyle(printerColor),
			SuccessPrinter:      successPrinter,
			FailPrinter:         errorPrinter,
			WarningPrinter:      warningPrinter,
			RemoveWhenDone:      false,
			ShowTimer:           true,
			TimerRoundingFactor: time.Second,
			TimerStyle:          &pterm.ThemeDefault.TimerStyle,
		},
	}

	pfo := &PortForwardOptions{
		Namespace: namespace,
		Selector:  &liqolabels.NetworkManagerPodLabelSelector,
		Config:    restConfig,
		Client:    localK8sClient,
		PortForwarder: &DefaultPortForwarder{
			genericclioptions.NewTestIOStreamsDiscard(),
		},
		RemotePort:   liqoconsts.NetworkManagerIpamPort,
		LocalPort:    0,
		Ports:        nil,
		StopChannel:  make(chan struct{}),
		ReadyChannel: make(chan struct{}),
	}

	return &Cluster{
		name:             name,
		namespace:        namespace,
		printer:          printer,
		locK8sClient:     localK8sClient,
		locCtrlRunClient: localCtrlRunClient,
		remCtrlRunClient: remoteCtrlRunClient,
		namespaceManager: tenantnamespace.NewTenantNamespaceManager(localK8sClient),
		PortForwardOpts:  pfo,
	}
}

func (c *Cluster) Init(ctx context.Context) error {
	// Get cluster identity.
	s, _ := c.printer.Spinner.Start("retrieving cluster identity")
	cm, err := liqogetters.GetConfigMapByLabel(ctx, c.locK8sClient, c.namespace, &liqolabels.ClusterIDConfigMapLabelSelector)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving cluster identity: %v", err))
		return err
	}
	clusterID, err := liqogetters.GetClusterIDFromConfigMap(cm)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving cluster identity: %v", err))
		return err
	}
	s.Success(fmt.Sprintf("cluster identity correctly retrieved"))

	// Get network configuration.
	s, _ = c.printer.Spinner.Start("retrieving network configuration")
	selector, err := metav1.LabelSelectorAsSelector(&liqolabels.IPAMStorageLabelSelector)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving network configuration: %v", err))
		return err
	}
	ipamStore, err := liqogetters.GetIPAMStorageByLabel(ctx, c.locCtrlRunClient, selector)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving network configuration: %v", err))
		return err
	}
	netcfg, err := liqonetutils.GetNetworkConfiguration(ipamStore)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving network configuration: %v", err))
		return err
	}
	s.Success("network configuration correctly retrieved")

	// Get vpn configuration.
	s, _ = c.printer.Spinner.Start("retrieving WireGuard configuration")
	svc, err := liqogetters.GetServiceByLabel(ctx, c.locK8sClient, c.namespace, &liqolabels.GatewayServiceLabelSelector)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving WireGuard configuration: %v", err))
		return err
	}
	ip, port, err := liqonetutils.GetWireGuardEndpointFromService(svc, liqoconsts.GatewayServiceAnnotationKey, wireguard.DriverName)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving WireGuard configuration: %v", err))
		return err
	}
	secret, err := liqogetters.GetSecretByLabel(ctx, c.locK8sClient, c.namespace, &liqolabels.WireGuardSecretLabelSelector)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving WireGuard configuration: %v", err))
		return err
	}
	pubKey, err := liqonetutils.RetrieveWGPubKeyFromSecret(secret, wireguard.PublicKey)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving WireGuard configuration: %v", err))
		return err
	}
	s.Success("wireGuard configuration correctly retrieved")

	// Get authentication token.
	s, _ = c.printer.Spinner.Start("retrieving authentication token")
	authToken, err := auth.GetToken(ctx, c.locCtrlRunClient, c.namespace)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving auth token: %v", err))
		return err
	}
	s.Success("authentication token correctly retrieved")

	// Get authentication endpoint.
	s, _ = c.printer.Spinner.Start("retrieving authentication  endpoint")
	svc, err = liqogetters.GetServiceByLabel(ctx, c.locK8sClient, c.namespace, &liqolabels.AuthServiceLabelSelector)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving authentication endpoint: %v", err))
		return err
	}
	// TODO: export port name of the auth endpoint as a constant.
	ipAuth, portAuth, err := liqogetters.GetAuthEndpointFromService(svc, "https")
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while retrieving authentication endpoint: %v", err))
		return err
	}
	s.Success("authentication endpoint correctly retrieved")

	// Set configuration
	c.clusterID = clusterID
	c.netConfig = netcfg

	c.wgConfig = &liqonetutils.WireGuardConfig{
		PubKey:       pubKey.String(),
		EndpointIP:   ip,
		EndpointPort: port,
		BackEndType:  wireguard.DriverName,
	}

	c.authToken = authToken

	c.authEP = &Endpoint{
		ip:   ipAuth,
		port: portAuth,
	}

	return nil
}

func (c *Cluster) GetClusterID() *discoveryv1alpha1.ClusterIdentity {
	return c.clusterID
}

func (c *Cluster) GetLocTenantNS() string {
	return c.locTenantNamespace
}

func (c *Cluster) SetRemTenantNS(remTenantNamespace string) {
	c.remTenantNamespace = remTenantNamespace
}

func (c *Cluster) GetAuthToken() string {
	return c.authToken
}

func (c *Cluster) GetAuthURL() string {
	return c.authEP.GetHTTPSURL()
}

func (c *Cluster) GetProxyURL() string {
	return c.proxyEP.GetHTTPURL()
}

func (c *Cluster) SetUpTenantNamespace(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity) error {
	s, _ := c.printer.Spinner.Start(fmt.Sprintf("creating tenant namespace for remote cluster {%s}", remoteClusterID.ClusterName))
	ns, err := c.namespaceManager.CreateNamespace(*remoteClusterID)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while creating tenant namespace for remote cluster {%s}: %v", remoteClusterID.ClusterName, err))
		return err
	}
	s.Success(fmt.Sprintf("tenant namespace {%s} created for remote cluster {%s}", ns.Name, remoteClusterID.ClusterName))
	c.locTenantNamespace = ns.Name
	return nil
}

func (c *Cluster) TearDownTenantNamespace(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity, timeout time.Duration) error {
	remName := remoteClusterID.ClusterName
	c.locTenantNamespace = tenantnamespace.GetNameForNamespace(*remoteClusterID)
	s, _ := c.printer.Spinner.Start(fmt.Sprintf("removing tenant namespace {%s} for remote cluster {%s}", c.locTenantNamespace, remName))
	_, err := c.locK8sClient.CoreV1().Namespaces().Get(ctx, c.locTenantNamespace, metav1.GetOptions{})
	if client.IgnoreNotFound(err) != nil {
		s.Fail(fmt.Sprintf("an error occurred while getting tenant namespace for remote cluster {%s}: %v", remName, err))
		return err
	}

	if err != nil {
		s.Warning(fmt.Sprintf("tenant namespace {%s} for remote cluster {%s} not found", c.locTenantNamespace, remName))
		return nil
	}

	deadLine := time.After(timeout)
	for {
		select {
		case <-deadLine:
			msg := fmt.Sprintf("timout (%.0fs) expired while waiting tenant namespace {%s} to be deleted",
				timeout.Seconds(), c.locTenantNamespace)
			s.Fail(msg)
			return fmt.Errorf(msg)
		default:
			err := c.locK8sClient.CoreV1().Namespaces().Delete(ctx, c.locTenantNamespace, metav1.DeleteOptions{})
			if client.IgnoreNotFound(err) != nil {
				s.Fail(fmt.Sprintf("an error occurred while deleting tenant namespace {%s} for remote cluster {%s}: %v", c.locTenantNamespace, remName, err))
				return err
			}

			if err != nil {
				s.Success(fmt.Sprintf("tenant namespace {%s} correctly removed for remote cluster {%s}", c.locTenantNamespace, remName))
				return nil
			}

			time.Sleep(2 * time.Second)
		}
	}
}

func (c *Cluster) ExchangeNetworkCfg(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity) error {
	// Enforce the network configuration in the local cluster.
	s, _ := c.printer.Spinner.Start("creating network configuration in local cluster")
	if err := c.enforceNetworkCfg(ctx, remoteClusterID, true); err != nil {
		s.Fail(fmt.Sprintf("an error occurred while creating network configuration in local cluster: %v", err))
		return err
	}
	s.Success(fmt.Sprintf("network configuration created in local cluster {%s}", c.clusterID.ClusterName))

	// Enforce the network configuration in the local cluster.
	s, _ = c.printer.Spinner.Start(fmt.Sprintf("creating network configuration in remote cluster {%s}", remoteClusterID.ClusterName))
	if err := c.enforceNetworkCfg(ctx, remoteClusterID, false); err != nil {
		s.Fail(fmt.Sprintf("an error occurred while creating network configuration in remote cluster: %v", err))
		return err
	}
	s.Success(fmt.Sprintf("network configuration created in remote cluster {%s}", remoteClusterID.ClusterName))

	// Wait for the network configuration to be processed by the remote cluster.
	s, _ = c.printer.Spinner.Start(fmt.Sprintf("waiting network configuration to be processed by remote cluster {%s}", remoteClusterID.ClusterName))
	netcfg, err := c.waitForNetCfg(ctx, remoteClusterID, 60*time.Second)
	if err != nil {
		s.Fail(err)
		return err
	}
	s.UpdateText(fmt.Sprintf("reflecting network configuration status from cluster {%s}", remoteClusterID.ClusterName))
	if err := c.reflectNetworkCfgStatus(ctx, remoteClusterID, &netcfg.Status); err != nil {
		s.Fail(err)
		return err
	}
	s.Success(fmt.Sprintf("network configuration status correctly reflected from cluster {%s}", remoteClusterID.ClusterName))
	return nil
}

func (c *Cluster) enforceNetworkCfg(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity, local bool) error {
	// Get the network config.
	netcfg, err := c.getNetworkCfg(ctx, remoteClusterID, local)
	if client.IgnoreNotFound(err) != nil {
		return err
	}

	if err != nil {
		return c.createNetworkCfg(ctx, remoteClusterID, local)
	}

	return c.updateNetworkCfgSpec(ctx, netcfg, remoteClusterID, local)
}

func (c *Cluster) getNetworkCfg(ctx context.Context,
	remoteClusterID *discoveryv1alpha1.ClusterIdentity, local bool) (*netv1alpha1.NetworkConfig, error) {
	var cl client.Client
	var selector labels.Selector
	var err error
	var ns string

	if local {
		cl = c.locCtrlRunClient
		if selector, err = labelSelectorForReplicatedResource(remoteClusterID, local); err != nil {
			return nil, err
		}
		ns = c.locTenantNamespace
	} else {
		cl = c.remCtrlRunClient
		if selector, err = labelSelectorForReplicatedResource(c.clusterID, local); err != nil {
			return nil, err
		}
		ns = c.remTenantNamespace
	}

	// Get the network config.
	return liqogetters.GetNetworkConfigByLabel(ctx, cl, ns, selector)
}

func (c *Cluster) createNetworkCfg(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity, local bool) error {
	netcfg := &netv1alpha1.NetworkConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      foreigncluster.UniqueName(remoteClusterID),
			Namespace: c.locTenantNamespace,
			Labels: map[string]string{
				liqoconsts.ReplicationRequestedLabel:   strconv.FormatBool(true),
				liqoconsts.ReplicationDestinationLabel: remoteClusterID.ClusterID,
			},
		},
	}
	c.populateNetworkCfg(netcfg, remoteClusterID, local)

	if local {
		return c.locCtrlRunClient.Create(ctx, netcfg)
	}

	return c.remCtrlRunClient.Create(ctx, netcfg)
}

func (c *Cluster) updateNetworkCfgSpec(ctx context.Context, netcfg *netv1alpha1.NetworkConfig,
	remoteClusterID *discoveryv1alpha1.ClusterIdentity, local bool) error {
	original := netcfg.DeepCopy()

	c.populateNetworkCfg(netcfg, remoteClusterID, local)

	if reflect.DeepEqual(original, netcfg) {
		return nil
	}

	if local {
		return c.locCtrlRunClient.Update(ctx, netcfg)
	}

	return c.remCtrlRunClient.Update(ctx, netcfg)
}

func (c *Cluster) updateNetworkCfgStatus(ctx context.Context, netcfg *netv1alpha1.NetworkConfig,
	newStatus *netv1alpha1.NetworkConfigStatus) error {
	if reflect.DeepEqual(netcfg.Status, newStatus) {
		return nil
	}

	netcfg.Status = *newStatus
	return c.locCtrlRunClient.Status().Update(ctx, netcfg)
}

func (c *Cluster) reflectNetworkCfgStatus(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity, status *netv1alpha1.NetworkConfigStatus) error {
	// Get the local networkconfig
	netcfg, err := c.getNetworkCfg(ctx, remoteClusterID, true)
	if err != nil {
		return nil
	}

	return c.updateNetworkCfgStatus(ctx, netcfg, status)
}

func (c *Cluster) waitForNetCfg(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity, timeout time.Duration) (*netv1alpha1.NetworkConfig, error) {
	deadLine := time.After(timeout)
	for {
		select {
		case <-deadLine:
			return nil, fmt.Errorf("timout (%.0fs) expired while waiting for cluster {%s} to process the networkconfig",
				timeout.Seconds(), remoteClusterID.ClusterName)
		default:
			netcfg, err := c.getNetworkCfg(ctx, remoteClusterID, false)
			if err != nil {
				return nil, err
			}
			if netcfg.Status.Processed {
				return netcfg, err
			}
			time.Sleep(2 * time.Second)
		}
	}
}

func (c *Cluster) populateNetworkCfg(netcfg *netv1alpha1.NetworkConfig, remoteClusterID *discoveryv1alpha1.ClusterIdentity, local bool) {
	clusterID := remoteClusterID.ClusterID

	if netcfg.Labels == nil {
		netcfg.Labels = map[string]string{}
	}

	netcfg.Labels[liqoconsts.ReplicationRequestedLabel] = strconv.FormatBool(true)
	netcfg.Labels[liqoconsts.ReplicationDestinationLabel] = clusterID

	if !local {
		// setting the right namespace in the remote cluster
		netcfg.Namespace = c.remTenantNamespace
		// setting the replication label to false
		netcfg.Labels[liqoconsts.ReplicationRequestedLabel] = strconv.FormatBool(false)
		// setting replication status to true
		netcfg.Labels[liqoconsts.ReplicationStatusLabel] = strconv.FormatBool(true)
		// setting originID i.e clusterID of home cluster
		netcfg.Labels[liqoconsts.ReplicationOriginLabel] = c.clusterID.ClusterID
	}

	netcfg.Spec.RemoteCluster = *remoteClusterID
	netcfg.Spec.PodCIDR = c.netConfig.PodCIDR
	netcfg.Spec.ExternalCIDR = c.netConfig.ExternalCIDR
	netcfg.Spec.EndpointIP = c.wgConfig.EndpointIP
	netcfg.Spec.BackendType = wireguard.DriverName

	if netcfg.Spec.BackendConfig == nil {
		netcfg.Spec.BackendConfig = map[string]string{}
	}

	netcfg.Spec.BackendConfig[wireguard.PublicKey] = c.wgConfig.PubKey
	netcfg.Spec.BackendConfig[wireguard.ListeningPort] = c.wgConfig.EndpointPort
}

func labelSelectorForReplicatedResource(clusterID *discoveryv1alpha1.ClusterIdentity, local bool) (labels.Selector, error) {
	if local {
		labelsSet := labels.Set{
			liqoconsts.ReplicationDestinationLabel: clusterID.ClusterID,
			liqoconsts.ReplicationRequestedLabel:   strconv.FormatBool(true),
		}

		return labels.ValidatedSelectorFromSet(labelsSet)
	}
	labelsSet := labels.Set{
		liqoconsts.ReplicationOriginLabel: clusterID.ClusterID,
		liqoconsts.ReplicationStatusLabel: strconv.FormatBool(true),
	}

	return labels.ValidatedSelectorFromSet(labelsSet)
}

func (c *Cluster) PortForwardIPAM(ctx context.Context) error {
	s, _ := c.printer.Spinner.Start("port-forwarding IPAM service")

	if err := c.PortForwardOpts.RunPortForward(ctx); err != nil {
		s.Fail(fmt.Sprintf("an error occurred while port-forwarding IPAM service: %v", err))
		return err
	}
	s.Success(fmt.Sprintf("IPAM service correctly port-forwarded {%s}", c.PortForwardOpts.Ports[0]))

	return nil
}

func (c *Cluster) StopPortForwardIPAM() {
	s, _ := c.printer.Spinner.Start("stopping IPAM service port-forward")
	c.PortForwardOpts.StopPortForward()
	s.Success(fmt.Sprintf("IPAM service port-forward correctly stopped {%s}", c.PortForwardOpts.Ports[0]))
}

func (c *Cluster) SetUpProxy(ctx context.Context) error {
	s, _ := c.printer.Spinner.Start(fmt.Sprintf("configuring proxy pod {%s} and service in namespace {%s}", proxyName, c.namespace))

	ep, err := createProxy(ctx, c.locK8sClient, proxyName, c.namespace)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while setting up proxy pod {%s} in namespace {%s}", proxyName, c.namespace))
		return err
	}
	s.Success(fmt.Sprintf("proxy pod {%s} correctly configured in namespace {%s}", proxyName, c.namespace))

	c.proxyEP = ep

	return nil
}

func (c *Cluster) RemoveProxy(ctx context.Context, timeout time.Duration) error {
	s, _ := c.printer.Spinner.Start(fmt.Sprintf("removing proxy pod {%s} and service in namespace {%s}", proxyName, c.namespace))

	if err := deleteProxy(ctx, c.locK8sClient, proxyName, c.namespace); err != nil {
		s.Fail(fmt.Sprintf("an error occurred while removing proxy pod {%s} in namespace {%s}", proxyName, c.namespace))
		return err
	}
	deadLine := time.After(timeout)
	for {
		select {
		case <-deadLine:
			msg := fmt.Sprintf("timout (%.0fs) expired while waiting for proxy pod to be deleted",
				timeout.Seconds())
			s.Fail(msg)
			return fmt.Errorf(msg)
		default:
			_, err := c.locK8sClient.CoreV1().Pods(c.namespace).Get(ctx, proxyName, metav1.GetOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				s.Fail(fmt.Sprintf("an error occurred while getting proxy pod: %v", err))
				return err
			} else if k8serrors.IsNotFound(err) {
				s.Success(fmt.Sprintf("proxy pod {%s} correctly removed in namespace {%s}", proxyName, c.namespace))
				return nil
			}
			time.Sleep(2 * time.Second)
		}
	}

	return nil
}

func (c *Cluster) MapProxyIPForCluster(ctx context.Context, ipamClient ipam.IpamClient, remoteCluster *discoveryv1alpha1.ClusterIdentity) error {
	clusterName := remoteCluster.ClusterName
	ipToBeRemapped := c.proxyEP.GetIP()

	s, _ := c.printer.Spinner.Start(fmt.Sprintf("mapping proxy ip {%s} for cluster {%s}", ipToBeRemapped, clusterName))

	ip, err := mapServiceForCluster(ctx, ipamClient, ipToBeRemapped, remoteCluster)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while mapping proxy address {%s} for cluster {%s}: %v", ipToBeRemapped, clusterName, err))
		return err
	}

	c.proxyEP.SetRemappedIP(ip)

	s.Success(fmt.Sprintf("proxy address {%s} remapped to {%s} for remote cluster {%s}", ipToBeRemapped, ip, clusterName))

	return nil
}

func (c *Cluster) MapAuthIPForCluster(ctx context.Context, ipamClient ipam.IpamClient, remoteCluster *discoveryv1alpha1.ClusterIdentity) error {
	clusterName := remoteCluster.ClusterName
	ipToBeRemapped := c.authEP.GetIP()

	s, _ := c.printer.Spinner.Start(fmt.Sprintf("mapping auth ip {%s} for cluster {%s}", ipToBeRemapped, clusterName))

	ip, err := mapServiceForCluster(ctx, ipamClient, ipToBeRemapped, remoteCluster)
	if err != nil {
		s.Fail(fmt.Sprintf("an error occurred while mapping auth address {%s} for cluster {%s}: %v", ipToBeRemapped, clusterName, err))
		return err
	}

	c.authEP.SetRemappedIP(ip)

	s.Success(fmt.Sprintf("auth address {%s} remapped to {%s} for remote cluster {%s}", ipToBeRemapped, ip, clusterName))

	return nil
}

func (c *Cluster) NewIPAMClient(ctx context.Context) (ipam.IpamClient, error) {
	ipamTarget := fmt.Sprintf("%s:%d", "localhost", c.PortForwardOpts.LocalPort)

	dialctx, cancel := context.WithTimeout(ctx, 10*time.Second)

	connection, err := grpc.DialContext(dialctx, ipamTarget, grpc.WithInsecure(), grpc.WithBlock())
	cancel()
	if err != nil {
		c.printer.Error.Printf("an error occurred while creating ipam client: %v")
		return nil, err
	}

	return ipam.NewIpamClient(connection), nil
}

func mapServiceForCluster(ctx context.Context, ipamClient ipam.IpamClient, ipToBeRemapped string, remoteCluster *discoveryv1alpha1.ClusterIdentity) (string, error) {
	mapRequest := &ipam.MapRequest{
		ClusterID: remoteCluster.ClusterID,
		Ip:        ipToBeRemapped,
	}

	resp, err := ipamClient.MapEndpointIP(ctx, mapRequest)
	if err != nil {
		return "", nil
	}

	return resp.Ip, nil
}

func (c *Cluster) EnforceForeignCluster(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity,
	token, authURL, proxyURL string) error {
	remID := remoteClusterID.ClusterID
	remName := remoteClusterID.ClusterName
	s, _ := c.printer.Spinner.Start(fmt.Sprintf("creating foreign cluster for the remote cluster {%s}", remName))
	if c.clusterID.ClusterID == remoteClusterID.ClusterID {
		msg := fmt.Sprintf("the clusterID {%s} of remote cluster {%s} is equal to the ID of the local cluster", remID, remName)
		s.Fail(msg)
		return fmt.Errorf(msg)
	}

	if err := authenticationtoken.StoreInSecret(ctx, c.locK8sClient, remID, token, c.namespace); err != nil {
		msg := fmt.Sprintf("an error occurred while storing auth token for remote cluster {%s}: %v", remName, err)
		s.Fail(msg)
		return fmt.Errorf(msg)
	}

	// Get existing foreign cluster if it does exist
	fc, err := foreigncluster.GetForeignClusterByID(ctx, c.locCtrlRunClient, remID)
	if client.IgnoreNotFound(err) != nil {
		s.Fail(fmt.Sprintf("an error occurred while getting foreign cluster for remote cluster {%s}: %v", remName, err))
		return err
	}

	// Not nil only if the foreign cluster does not exist.
	if err != nil {
		fc = &discoveryv1alpha1.ForeignCluster{ObjectMeta: metav1.ObjectMeta{Name: remName,
			Labels: map[string]string{discovery.ClusterIDLabel: remID}}}
	}

	if _, err = controllerutil.CreateOrPatch(ctx, c.locCtrlRunClient, fc, func() error {
		fc.Spec.ForeignAuthURL = authURL
		fc.Spec.ForeignProxyURL = proxyURL
		fc.Spec.OutgoingPeeringEnabled = discoveryv1alpha1.PeeringEnabledYes
		fc.Spec.NetworkingEnabled = discoveryv1alpha1.NetworkingEnabledNo
		if fc.Spec.IncomingPeeringEnabled == "" {
			fc.Spec.IncomingPeeringEnabled = discoveryv1alpha1.PeeringEnabledAuto
		}
		if fc.Spec.InsecureSkipTLSVerify == nil {
			fc.Spec.InsecureSkipTLSVerify = pointer.BoolPtr(true)
		}
		return nil
	}); err != nil {
		s.Fail(fmt.Sprintf("an error occurred while creating/updating foreign cluster for remote cluster {%s}: %v", remName, err))
		return err
	}
	s.Success(fmt.Sprintf("foreign cluster for remote cluster {%s} correctly configured", remName))
	return nil
}

func (c *Cluster) DeleteForeignCluster(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity) error {
	remID := remoteClusterID.ClusterID
	remName := remoteClusterID.ClusterName

	s, _ := c.printer.Spinner.Start(fmt.Sprintf("deleting foreigncluster for the remote cluster {%s}", remName))
	if c.clusterID.ClusterID == remoteClusterID.ClusterID {
		msg := fmt.Sprintf("the clusterID {%s} of remote cluster {%s} is equal to the ID of the local cluster", remID, remName)
		s.Fail(msg)
		return fmt.Errorf(msg)
	}

	// Get existing foreign cluster if it does exist
	fc, err := foreigncluster.GetForeignClusterByID(ctx, c.locCtrlRunClient, remID)
	if client.IgnoreNotFound(err) != nil {
		s.Fail(fmt.Sprintf("an error occurred while getting foreign cluster for remote cluster {%s}: %v", remName, err))
		return err
	}

	if k8serrors.IsNotFound(err) {
		s.Warning(fmt.Sprintf("it seems that the foreign cluster for remote cluster {%s} has been already removed", remName))
		return nil
	}

	if err := c.locCtrlRunClient.Delete(ctx, fc); err != nil {
		s.Fail(fmt.Sprintf("an error occurred while deleting foreigncluster for remote cluster {%s}: %v", remName, err))
		return err
	}

	s.Success(fmt.Sprintf("foreigncluster deleted for remote cluster {%s}", remName))
	return nil
}

func (c *Cluster) DisablePeering(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity) error {
	remID := remoteClusterID.ClusterID
	remName := remoteClusterID.ClusterName
	s, _ := c.printer.Spinner.Start(fmt.Sprintf("disabling peering for the remote cluster {%s}", remName))
	if c.clusterID.ClusterID == remoteClusterID.ClusterID {
		msg := fmt.Sprintf("the clusterID {%s} of remote cluster {%s} is equal to the ID of the local cluster", remID, remName)
		s.Fail(msg)
		return fmt.Errorf(msg)
	}

	// Get existing foreign cluster if it does exist
	fc, err := foreigncluster.GetForeignClusterByID(ctx, c.locCtrlRunClient, remID)
	if client.IgnoreNotFound(err) != nil {
		s.Fail(fmt.Sprintf("an error occurred while getting foreign cluster for remote cluster {%s}: %v", remName, err))
		return err
	}

	// Not nil only if the foreign cluster does not exist.
	if err != nil {
		s.Warning(fmt.Sprintf("it seems that the foreign cluster for remote cluster {%s} has been already removed", remName))
		return nil
	}

	// Set outgoing peering to no.
	if _, err := controllerutil.CreateOrPatch(ctx, c.locCtrlRunClient, fc, func() error {
		fc.Spec.OutgoingPeeringEnabled = "No"
		return nil
	}); err != nil {
		s.Fail(fmt.Sprintf("an error occurred withe disabling peering for remote cluster {%s}: %v", remName, err))
		return err
	}
	s.Success(fmt.Sprintf("peering correctly disabled for remote cluster {%s}", remName))

	return nil
}

func (c *Cluster) WaitForUnpeering(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity, timeout time.Duration) error {
	remName := remoteClusterID.ClusterName
	s, _ := c.printer.Spinner.Start(fmt.Sprintf("waiting for event {%s} from the remote cluster {%s}", UnpeeringEvent, remName))
	err := WaitForEventOnForeignCluster(ctx, remoteClusterID, UnpeeringEvent, UnpeerChecker, timeout, c.locCtrlRunClient)
	if err != nil {
		s.Fail(err.Error())
		return err
	}
	s.Success(fmt.Sprintf("event {%s} successfully occurred for remote cluster {%s}", UnpeeringEvent, remName))
	return nil
}

func (c *Cluster) WaitForAuth(ctx context.Context, remoteClusterID *discoveryv1alpha1.ClusterIdentity, timeout time.Duration) error {
	remName := remoteClusterID.ClusterName
	s, _ := c.printer.Spinner.Start(fmt.Sprintf("waiting for event {%s} from the remote cluster {%s}", AuthEvent, remName))
	err := WaitForEventOnForeignCluster(ctx, remoteClusterID, AuthEvent, AuthChecker, timeout, c.locCtrlRunClient)
	if err != nil {
		s.Fail(err.Error())
		return err
	}
	s.Success(fmt.Sprintf("event {%s} successfully occurred for remote cluster {%s}", AuthEvent, remName))
	return nil
}
