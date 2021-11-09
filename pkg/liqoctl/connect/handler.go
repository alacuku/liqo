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
	"context"
	"fmt"
)

// Args flags of the connect command.
type Args struct {
	Cluster1Namespace  string
	Cluster2Namespace  string
	Cluster1Kubeconfig string
	Cluster2Kubeconfig string
}

// Handler implements the logic of the connect command.
func (a *Args) Handler(ctx context.Context) error {
	// Check that the kubeconfigs are different.
	if a.Cluster1Kubeconfig == a.Cluster2Kubeconfig {
		return fmt.Errorf("kubeconfig1 and kubeconfig2 has to be different, current value: %s", a.Cluster2Kubeconfig)
	}
	cluster1, err := NewCluster(a.Cluster1Kubeconfig, a.Cluster1Namespace)
	if err != nil {
		return err
	}
	if err := cluster1.Init(); err != nil {
		return err
	}
	defer cluster1.Stop()

	cluster2, err := NewCluster(a.Cluster2Kubeconfig, a.Cluster2Namespace)
	if err != nil {
		return err
	}

	if err := cluster2.Init(); err != nil {
		return err
	}
	defer cluster2.Stop()

	// Map cluster2 in cluster1.
	mapping2To1, err := cluster1.mapCluster(cluster2.netConfig)
	if err != nil {
		return err
	}

	// Map cluster1 in cluster2.
	mapping1To2, err := cluster2.mapCluster(cluster1.netConfig)
	if err != nil {
		return err
	}

	// Create TEP in cluster1 for cluster2.
	if err := cluster1.CreateTEP(&cluster2.netConfig, mapping2To1, mapping1To2); err != nil {
		return err
	}

	// Create TEP in cluster1 for cluster2.
	if err := cluster2.CreateTEP(&cluster1.netConfig, mapping1To2, mapping2To1); err != nil {
		return err
	}

	if err := cluster1.setUpProxy(ctx); err != nil{
		return err
	}

	if err := cluster2.setUpProxy(ctx); err != nil{
		return err
	}

	// Map ip proxy of cluster1 into cluster2.
	proxyIPcluster2AsSeenByCluster1, err := cluster1.MapIP(cluster2.netConfig.ClusterID, cluster1.proxyIP)
	if err != nil{
		return err
	}

	proxyIPcluster1AsSeenByCluster2, err := cluster2.MapIP(cluster1.netConfig.ClusterID, cluster2.proxyIP)
	if err != nil{
		return err
	}
	fmt.Printf(proxyIPcluster2AsSeenByCluster1.Ip, proxyIPcluster1AsSeenByCluster2.Ip)


	return nil
}
