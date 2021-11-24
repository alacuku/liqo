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

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/liqotech/liqo/internal/liqonet/network-manager/tunnelendpointcreator"
	"github.com/liqotech/liqo/pkg/discovery"
	"github.com/liqotech/liqo/pkg/liqonet/ipam"
	"github.com/liqotech/liqo/pkg/liqonet/tunnel/wireguard"
)

const (
	timeout = time.Second * 60
)

type HTTPServer struct {
	GetDynamicConfig func() (ip, port, pubKey string)
	WaitConfig       func(ctx context.Context) bool
	Tec              *tunnelendpointcreator.TunnelEndpointCreator
	ClientSet        k8s.Interface
	NetConfig        NetworkConfiguration
	Ipam             *ipam.IPAM
}

type NetworkConfiguration struct {
	ClusterID       string            `json:"clusterID"`
	PodCIDR         string            `json:"podCIDR"`
	ServiceCIDR     string            `json:"serviceCIDR"`
	ExternalCIDR    string            `json:"externalCIDR"`
	ReservedSubnets []string          `json:"reservedSubnets"`
	Pools           []string          `json:"pools"`
	VPNBackend      string            `json:"VPNBackend"`
	EndpointIP      string            `json:"endpointIP"`
	BackendConfig   map[string]string `json:"backendConfig"`
}

type TunnelEndpointSpec struct {
	ClusterID             string            `json:"clusterID"`
	LocalPodCIDR          string            `json:"localPodCIDR"`
	LocalNATPodCIDR       string            `json:"localNATPodCIDR"`
	LocalExternalCIDR     string            `json:"localExternalCIDR"`
	LocalNATExternalCIDR  string            `json:"localNATExternalCIDR"`
	RemotePodCIDR         string            `json:"remotePodCIDR"`
	RemoteNATPodCIDR      string            `json:"remoteNATPodCIDR"`
	RemoteExternalCIDR    string            `json:"remoteExternalCIDR"`
	RemoteNATExternalCIDR string            `json:"remoteNATExternalCIDR"`
	VPNBackend            string            `json:"VPNBackend"`
	EndpointIP            string            `json:"endpointIP"`
	BackendConfig         map[string]string `json:"backendConfig"`
}

type ClusterMappingReq struct {
	ClusterID       string `json:"clusterID"`
	PodCIDR         string `json:"podCIDR"`
	ExternalCIDR    string `json:"externalCIDR"`
	PodCIDRNAT      string `json:"podCIDRNAT"`
	ExternalCIDRNAT string `json:"externalCIDRNAT"`
}

type MapRequest struct {
	ClusterID string `json:"clusterID"`
	Ip        string `json:"ip"`
}

type MapResponse struct {
	Ip string `json:"ip"`
}

type ClusterMappingList struct {
	Items []ClusterMappingReq `json:"items"`
}

func (s *HTTPServer) Start(ctx context.Context) {
	go func() {
		// Wait for the configuration to be available.
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		if !s.WaitConfig(timeoutCtx) {
			klog.Fatal("unable to start http server: context expired before initialization completed for the secret and service watchers")
		}

		router := mux.NewRouter().StrictSlash(true)
		router.HandleFunc("/config", s.networkConfiguration).Methods(http.MethodGet)
		router.HandleFunc("/clusters", s.mapCluster).Methods(http.MethodPost)
		router.HandleFunc("/tep", s.enforceTEP).Methods(http.MethodPost)
		router.HandleFunc("/ip", s.mapIP).Methods(http.MethodPost)
		router.HandleFunc("/removetep", s.removeTEP).Methods(http.MethodDelete)

		if err := http.ListenAndServe(":8080", router); err != nil {
			klog.Fatalf("unable to start http server: %v", err)
		}
	}()
}

func (s *HTTPServer) networkConfiguration(w http.ResponseWriter, r *http.Request) {
	ip, port, pubKey := s.GetDynamicConfig()
	s.NetConfig.EndpointIP = ip
	s.NetConfig.BackendConfig = map[string]string{wireguard.PublicKey: pubKey, wireguard.ListeningPort: port}
	s.NetConfig.VPNBackend = wireguard.DriverName
	if err := json.NewEncoder(w).Encode(s.NetConfig); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (s *HTTPServer) mapCluster(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	dec := json.NewDecoder(strings.NewReader(string(body)))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	req := new(ClusterMappingReq)
	if err := dec.Decode(req); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	p, e, err := s.Ipam.GetSubnetsPerCluster(req.PodCIDR, req.ExternalCIDR, req.ClusterID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("%s/%s", r.RequestURI, req.ClusterID))
	w.WriteHeader(http.StatusCreated)
	req.PodCIDRNAT = p
	req.ExternalCIDRNAT = e
	if err := json.NewEncoder(w).Encode(req); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}

	return
}

func (s *HTTPServer) mapIP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	dec := json.NewDecoder(strings.NewReader(string(body)))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	req := new(MapRequest)
	if err := dec.Decode(req); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	resp, err := s.Ipam.MapEndpointIP(context.TODO(), &ipam.MapRequest{
		ClusterID: req.ClusterID,
		Ip:        req.Ip,
	})

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	mapResp := new(MapResponse)
	mapResp.Ip = resp.Ip
	if err := json.NewEncoder(w).Encode(mapResp); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
	}

	return
}

/*func (s *HTTPServer) getClusters(w http.ResponseWriter, r *http.Request) {



	req := new(ClusterMappingReq)
	if err := dec.Decode(req); err != nil{
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.Ipam.

	p, e, err := s.Ipam.GetSubnetsPerCluster(req.PodCIDR, req.ExternalCIDR, req.ClusterID)
	if err != nil{
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("%s/%s", r.RequestURI, req.ClusterID))
	w.WriteHeader(http.StatusCreated)
	req.PodCIDRNAT = p
	req.ExternalCIDRNAT = e
	if err := json.NewEncoder(w).Encode(req); err != nil{
		w.WriteHeader(http.StatusInternalServerError)
	}

	return
}*/

func (s *HTTPServer) enforceTEP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	dec := json.NewDecoder(strings.NewReader(string(body)))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	req := new(TunnelEndpointSpec)
	if err := dec.Decode(req); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	param := &tunnelendpointcreator.NetworkParam{
		RemoteClusterID:       req.ClusterID,
		RemoteEndpointIP:      req.EndpointIP,
		RemotePodCIDR:         req.RemotePodCIDR,
		RemoteNatPodCIDR:      req.RemoteNATPodCIDR,
		RemoteExternalCIDR:    req.RemoteExternalCIDR,
		RemoteNatExternalCIDR: req.RemoteNATExternalCIDR,
		LocalEndpointIP:       "",
		LocalNatPodCIDR:       req.LocalNATPodCIDR,
		LocalPodCIDR:          req.LocalPodCIDR,
		LocalExternalCIDR:     req.LocalExternalCIDR,
		LocalNatExternalCIDR:  req.LocalNATExternalCIDR,
		BackendType:           wireguard.DriverName,
		BackendConfig:         req.BackendConfig,
	}

	if err := s.Tec.IPManager.AddLocalSubnetsPerCluster(param.LocalNatPodCIDR, param.LocalNatExternalCIDR, param.RemoteClusterID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}
	nsName := "liqo-tenant-" + req.ClusterID
	tenantNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nsName,
			Labels: map[string]string{discovery.ClusterIDLabel: req.ClusterID, discovery.TenantNamespaceLabel: "true"},
		},
	}

	// First create namespace for the tenant.
	tenantNs, err = s.ClientSet.CoreV1().Namespaces().Create(context.TODO(), tenantNs, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	// Try to get the tunnelEndpoint, which may not exist
	_, found, err := s.Tec.GetTunnelEndpoint(context.TODO(), param.RemoteClusterID, nsName)
	if err != nil {
		klog.Errorf("an error occurred while getting resource tunnelEndpoint for cluster %s: %s", param.RemoteClusterID, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !found {
		if err := s.Tec.CreateTunnelEndpoint(context.TODO(), param, nil, nsName); err != nil {
			klog.Errorf("an error occurred while creating resource tunnelEndpoint for cluster %s: %s", param.RemoteClusterID, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		return
	}

	if err := s.Tec.UpdateSpecTunnelEndpoint(context.TODO(), param, nsName); err != nil {
		klog.Errorf("an error occurred while enforcing resource tunnelEndpoint for cluster %s: %s", param.RemoteClusterID, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	return
}

func (s *HTTPServer) removeTEP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	dec := json.NewDecoder(strings.NewReader(string(body)))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	req := new(TunnelEndpointSpec)
	if err := dec.Decode(req); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	// first remove mapping.
	if err := s.Tec.IPManager.RemoveClusterConfig(req.ClusterID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}
	nsName := "liqo-tenant-" + req.ClusterID

	// Delete namespace for the tenant.
	err = s.ClientSet.CoreV1().Namespaces().Delete(context.TODO(), nsName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		w.WriteHeader(http.StatusInternalServerError)
		klog.Error(err)
		return
	}

	return
}
