// Copyright 2019-2022 The Liqo Authors
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

package utils

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"inet.af/netaddr"
	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	netv1alpha1 "github.com/liqotech/liqo/apis/net/v1alpha1"
	"github.com/liqotech/liqo/internal/utils/errdefs"
	"github.com/liqotech/liqo/pkg/consts"
	liqoneterrors "github.com/liqotech/liqo/pkg/liqonet/errors"
)

var (
	// ShutdownSignals signals used to terminate the programs.
	ShutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGKILL}
)

type NetworkConfig struct {
	PodCIDR         string
	ExternalCIDR    string
	ServiceCIDR     string
	ReservedSubnets []string
}

type WireGuardConfig struct {
	PubKey       string
	EndpointIP   string
	EndpointPort string
	BackEndType  string
}

// MapIPToNetwork creates a new IP address obtained by means of the old IP address and the new network.
func MapIPToNetwork(newNetwork, oldIP string) (newIP string, err error) {
	if newNetwork == consts.DefaultCIDRValue {
		return oldIP, nil
	}
	// Parse newNetwork
	ip, network, err := net.ParseCIDR(newNetwork)
	if err != nil {
		return "", err
	}
	// Get mask
	mask := network.Mask
	// Get slice of bytes for newNetwork
	// Type net.IP has underlying type []byte
	parsedNewIP := ip.To4()
	// Get oldIP as slice of bytes
	parsedOldIP := net.ParseIP(oldIP)
	if parsedOldIP == nil {
		return "", fmt.Errorf("cannot parse oldIP")
	}
	parsedOldIP = parsedOldIP.To4()
	// Substitute the last 32-mask bits of newNetwork with bits taken by the old ip
	for i := 0; i < len(mask); i++ {
		// Step 1: NOT(mask[i]) = mask[i] ^ 0xff. They are the 'host' bits
		// Step 2: BITWISE AND between the host bits and parsedOldIP[i] zeroes the network bits in parsedOldIP[i]
		// Step 3: BITWISE OR copies the result of step 2 in newIP[i]
		parsedNewIP[i] |= (mask[i] ^ 0xff) & parsedOldIP[i]
	}
	newIP = parsedNewIP.String()
	return
}

func GetPodIP() (net.IP, error) {
	ipAddress, isSet := os.LookupEnv("POD_IP")
	if !isSet {
		return nil, errdefs.NotFound("the pod IP is not set")
	}
	if ipAddress == "" {
		return nil, errors.New("pod IP is not yet set")
	}
	return net.ParseIP(ipAddress), nil
}

// GetPodNamespace gets the namespace of the pod passed as an environment variable.
func GetPodNamespace() (string, error) {
	namespace, isSet := os.LookupEnv("POD_NAMESPACE")
	if !isSet {
		return "", errdefs.NotFound("the POD_NAMESPACE environment variable is not set as an environment variable")
	}
	return namespace, nil
}

// GetNodeName gets the name of the node where the pod is running passed as an environment variable.
func GetNodeName() (string, error) {
	nodeName, isSet := os.LookupEnv("NODE_NAME")
	if !isSet {
		return nodeName, errdefs.NotFound("NODE_NAME environment variable has not been set. check you manifest file")
	}
	return nodeName, nil
}

// GetMask retrieves the mask from a CIDR.
func GetMask(network string) uint8 {
	_, subnet, err := net.ParseCIDR(network)
	utilruntime.Must(err)
	ones, _ := subnet.Mask.Size()
	return uint8(ones)
}

// SetMask forges a new cidr from a network cidr and a mask.
func SetMask(network string, mask uint8) string {
	_, n, err := net.ParseCIDR(network)
	utilruntime.Must(err)
	newMask := net.CIDRMask(int(mask), 32)
	n.Mask = newMask
	return n.String()
}

// Next used to get the second half of a given network.
func Next(network string) string {
	prefix, err := netaddr.ParseIPPrefix(network)
	utilruntime.Must(err)
	// Step 1: Get last IP address of network
	// Step 2: Get next IP address
	firstIP := prefix.Range().To.Next()
	prefix.IP = firstIP
	return prefix.String()
}

// GetPodCIDRS for a given tep the function retrieves the values for localPodCIDR and remotePodCIDR.
// Their values depend if the NAT is required or not.
func GetPodCIDRS(tep *netv1alpha1.TunnelEndpoint) (localRemappedPodCIDR, remotePodCIDR string) {
	if tep.Spec.RemoteNATPodCIDR != consts.DefaultCIDRValue {
		remotePodCIDR = tep.Spec.RemoteNATPodCIDR
	} else {
		remotePodCIDR = tep.Spec.RemotePodCIDR
	}
	localRemappedPodCIDR = tep.Spec.LocalNATPodCIDR
	return localRemappedPodCIDR, remotePodCIDR
}

// GetExternalCIDRS for a given tep the function retrieves the values for localExternalCIDR and remoteExternalCIDR.
// Their values depend if the NAT is required or not.
func GetExternalCIDRS(tep *netv1alpha1.TunnelEndpoint) (localExternalCIDR, remoteExternalCIDR string) {
	if tep.Spec.LocalNATExternalCIDR != consts.DefaultCIDRValue {
		localExternalCIDR = tep.Spec.LocalNATExternalCIDR
	} else {
		localExternalCIDR = tep.Spec.LocalExternalCIDR
	}
	if tep.Spec.RemoteNATExternalCIDR != consts.DefaultCIDRValue {
		remoteExternalCIDR = tep.Spec.RemoteNATExternalCIDR
	} else {
		remoteExternalCIDR = tep.Spec.RemoteExternalCIDR
	}
	return
}

// IsValidCIDR returns an error if the received CIDR is invalid.
func IsValidCIDR(cidr string) error {
	_, _, err := net.ParseCIDR(cidr)
	return err
}

// GetFirstIP returns the first IP address of a network.
func GetFirstIP(network string) (string, error) {
	firstIP, _, err := net.ParseCIDR(network)
	if err != nil {
		return "", err
	}
	return firstIP.String(), nil
}

// CheckTep checks validity of TunnelEndpoint resource fields.
func CheckTep(tep *netv1alpha1.TunnelEndpoint) error {
	if tep.Spec.ClusterID == "" {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.ClusterIDLabelName,
			Reason:    liqoneterrors.StringNotEmpty,
		}
	}
	if err := IsValidCIDR(tep.Spec.RemotePodCIDR); err != nil {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.PodCIDR,
			Reason:    liqoneterrors.ValidCIDR,
		}
	}
	if err := IsValidCIDR(tep.Spec.RemoteExternalCIDR); err != nil {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.ExternalCIDR,
			Reason:    liqoneterrors.ValidCIDR,
		}
	}
	if err := IsValidCIDR(tep.Spec.LocalPodCIDR); err != nil {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.LocalPodCIDR,
			Reason:    liqoneterrors.ValidCIDR,
		}
	}
	if err := IsValidCIDR(tep.Spec.LocalExternalCIDR); err != nil {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.LocalExternalCIDR,
			Reason:    liqoneterrors.ValidCIDR,
		}
	}
	if err := IsValidCIDR(tep.Spec.LocalNATPodCIDR); tep.Spec.LocalNATPodCIDR != consts.DefaultCIDRValue &&
		err != nil {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.LocalNATPodCIDR,
			Reason:    liqoneterrors.ValidCIDR,
		}
	}
	if err := IsValidCIDR(tep.Spec.LocalNATExternalCIDR); tep.Spec.LocalNATExternalCIDR != consts.DefaultCIDRValue &&
		err != nil {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.LocalNATExternalCIDR,
			Reason:    liqoneterrors.ValidCIDR,
		}
	}
	if err := IsValidCIDR(tep.Spec.RemoteNATPodCIDR); tep.Spec.RemoteNATPodCIDR != consts.DefaultCIDRValue &&
		err != nil {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.RemoteNATPodCIDR,
			Reason:    liqoneterrors.ValidCIDR,
		}
	}
	if err := IsValidCIDR(tep.Spec.RemoteNATExternalCIDR); tep.Spec.RemoteNATExternalCIDR != consts.DefaultCIDRValue &&
		err != nil {
		return &liqoneterrors.WrongParameter{
			Parameter: consts.RemoteNATExternalCIDR,
			Reason:    liqoneterrors.ValidCIDR,
		}
	}

	return nil
}

// GetOverlayIP given an IP address it is mapped in to the overlay network,
// described by consts.OverlayNetworkPrefix. It uses the overlay prefix and the
// last three octets of the original IP address.
func GetOverlayIP(ip string) string {
	addr := net.ParseIP(ip)
	// If the ip is malformed we prevent a panic, the subsequent calls
	// that use the returned value will return an error.
	if addr == nil {
		return ""
	}
	tokens := strings.Split(ip, ".")
	return strings.Join([]string{consts.OverlayNetworkPrefix, tokens[1], tokens[2], tokens[3]}, ".")
}

// AddAnnotationToObj for a given object it adds the annotation with the given key and value.
// It return a bool which is true when the annotations has been added or false if the
// annotation is already present.
func AddAnnotationToObj(obj client.Object, aKey, aValue string) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	oldAnnValue, ok := annotations[aKey]
	// If the annotations does not exist or is outdated then set it.
	if !ok || oldAnnValue != aValue {
		annotations[aKey] = aValue
		obj.SetAnnotations(annotations)
		return true
	}
	return false
}

// GetAnnotationValueFromObj for a given object it return the value of the label denoted by the
// given key. If the key does not exist it returns an empty string.
func GetAnnotationValueFromObj(obj client.Object, akey string) string {
	if obj.GetAnnotations() == nil {
		return ""
	}
	return obj.GetAnnotations()[akey]
}

// AddLabelToObj for a given object it adds the label with the given key and value.
// It return a bool which is true when the label has been added or false if the
// label is already present.
func AddLabelToObj(obj client.Object, labelKey, labelValue string) bool {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string, 1)
	}
	oldLabelValue, ok := labels[labelKey]
	// If the labels does not exist or is outdated then set it.
	if !ok || oldLabelValue != labelValue {
		labels[labelKey] = labelValue
		obj.SetLabels(labels)
		return true
	}
	return false
}

// GetLabelValueFromObj for a given object it return the value of the label denoted by the
// given key. If the key does not exist it returns an empty string.
func GetLabelValueFromObj(obj client.Object, labelKey string) string {
	if obj.GetLabels() == nil {
		return ""
	}
	return obj.GetLabels()[labelKey]
}

// DeleteIFaceByName deletes the interface that has the given name.
func DeleteIFaceByName(ifaceName string) error {
	existingIface, err := netlink.LinkByName(ifaceName)
	if err != nil {
		if errors.As(err, &netlink.LinkNotFoundError{}) {
			return nil
		}
		klog.Errorf("an error occurred while getting network interface {%s}: %v", ifaceName, err)
		return err
	}
	// Remove the existing network interface.
	if err = netlink.LinkDel(existingIface); err != nil && errors.As(err, &netlink.LinkNotFoundError{}) {
		if errors.As(err, &netlink.LinkNotFoundError{}) {
			return nil
		}
		klog.Errorf("an error occurred while deleting network interface {%s}: %v", existingIface.Attrs().Name, err)
		return err
	}
	return nil
}

// SplitNetwork returns the two halves that make up a given network.
func SplitNetwork(network string) []string {
	halves := make([]string, 2)

	// Get halves mask length.
	mask := GetMask(network)
	mask++

	// Get first half CIDR.
	halves[0] = SetMask(network, mask)

	// Get second half CIDR.
	halves[1] = Next(halves[0])

	return halves
}

// GetNetworkConfiguration returns the podCIDR, serviceCIDR, reservedSubnets and the externalCIDR
// as saved in the ipams.net.liqo.io custom resource instance.
// todo: return more explicative errors
func GetNetworkConfiguration(ipamS *netv1alpha1.IpamStorage) (*NetworkConfig, error) {
	if ipamS.Spec.PodCIDR == "" {
		return nil, fmt.Errorf("podCIDR is not set")
	}

	if ipamS.Spec.ServiceCIDR == "" {
		return nil, fmt.Errorf("serviceCIDR is not set")
	}

	if ipamS.Spec.ExternalCIDR == "" {
		return nil, fmt.Errorf("externalCIDR is not set")
	}

	return &NetworkConfig{
		PodCIDR:         ipamS.Spec.PodCIDR,
		ServiceCIDR:     ipamS.Spec.ServiceCIDR,
		ExternalCIDR:    ipamS.Spec.ExternalCIDR,
		ReservedSubnets: ipamS.Spec.ReservedSubnets,
	}, nil
}

// RetrieveWGEPFromNodePort retrieves the WireGuard endpoint from a NodePort service.
func RetrieveWGEPFromNodePort(service *corev1.Service, annotationKey, portName string) (endpointIP, endpointPort string, err error) {
	// Check if the node's IP where the gatewayPod is running has been set
	endpointIP, found := service.GetAnnotations()[annotationKey]
	if !found {
		err = fmt.Errorf("the node IP where the gateway pod is running has not yet been set as an annotation for service %q", klog.KObj(service))
		return endpointIP, endpointPort, err
	}

	// Check if the nodePort for wireguard has been set
	// TODO: move the code that extracts the port in a separate function.
	for _, port := range service.Spec.Ports {
		if port.Name == portName {
			if port.NodePort == 0 {
				err = fmt.Errorf("the NodePort for service %s has not yet been set", klog.KObj(service))
				return endpointIP, endpointPort, err
			}
			endpointPort = strconv.FormatInt(int64(port.NodePort), 10)
			return endpointIP, endpointPort, nil
		}
	}

	err = fmt.Errorf("port %s not found in service %q", portName, klog.KObj(service))
	return endpointIP, endpointPort, err
}

// RetrieveWGEPFromLoadBalancer retrieves the WireGuard endpoint from a LoadBalancer service.
func RetrieveWGEPFromLoadBalancer(service *corev1.Service, portName string) (endpointIP, endpointPort string, err error) {
	// Check if the ingress IP has been set.
	if len(service.Status.LoadBalancer.Ingress) == 0 {
		err = fmt.Errorf("the ingress IP has not been set for service %q of type %s", klog.KObj(service), service.Spec.Type)
		return endpointIP, endpointPort, err
	}

	// Retrieve the endpoint address
	if service.Status.LoadBalancer.Ingress[0].IP != "" {
		endpointIP = service.Status.LoadBalancer.Ingress[0].IP
	} else if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
		endpointIP = service.Status.LoadBalancer.Ingress[0].Hostname
	}

	// Retrieve the endpoint port
	// TODO: move the code that extracts the port in a separate function.
	for _, port := range service.Spec.Ports {
		if port.Name == portName {
			endpointPort = strconv.FormatInt(int64(port.Port), 10)
			return endpointIP, endpointPort, nil
		}
	}

	err = fmt.Errorf("port %s not found in service %q", portName, klog.KObj(service))
	return endpointIP, endpointPort, err
}

func GetWireGuardEndpointFromService(service *corev1.Service, annotationKey, portName string) (endpointIP, endpointPort string, err error) {
	switch service.Spec.Type {
	case corev1.ServiceTypeNodePort:
		return RetrieveWGEPFromNodePort(service, annotationKey, portName)

	case corev1.ServiceTypeLoadBalancer:
		return RetrieveWGEPFromLoadBalancer(service, portName)
	default:
		klog.Errorf("Service %q is of type %s, only types of %s and %s are accepted",
			klog.KObj(service), service.Spec.Type, corev1.ServiceTypeLoadBalancer, corev1.ServiceTypeNodePort)
		return "", "", fmt.Errorf("service %q is of type %s, only types of %s and %s are accepted",
			klog.KObj(service), service.Spec.Type, corev1.ServiceTypeLoadBalancer, corev1.ServiceTypeNodePort)
	}
}

// RetrieveWGPubKeyFromSecret retrieves the WireGuard public key from a given secret if present.
func RetrieveWGPubKeyFromSecret(secret *corev1.Secret, keyName string) (pubKey wgtypes.Key, err error) {
	// Extract the public key from the secret
	pubKeyByte, found := secret.Data[keyName]
	if !found {
		err = fmt.Errorf("no data with key %s found in secret %q", keyName, klog.KObj(secret))
		return pubKey, err
	}
	pubKey, err = wgtypes.ParseKey(string(pubKeyByte))
	if err != nil {
		err = fmt.Errorf("secret %q: invalid public key: %w", klog.KObj(secret), err)
		return pubKey, err
	}

	return pubKey, nil
}
