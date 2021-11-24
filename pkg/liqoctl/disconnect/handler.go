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

package disconnect

import (
	"fmt"
	"sync"

	"golang.org/x/net/context"

	"github.com/liqotech/liqo/pkg/liqoctl/connect"
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
	cluster1, err := connect.NewCluster(a.Cluster1Kubeconfig, a.Cluster1Namespace)
	if err != nil {
		return err
	}
	if err := cluster1.Init(); err != nil {
		return err
	}
	defer cluster1.Stop()

	cluster2, err := connect.NewCluster(a.Cluster2Kubeconfig, a.Cluster2Namespace)
	if err != nil {
		return err
	}

	if err := cluster2.Init(); err != nil {
		return err
	}
	defer cluster2.Stop()

	var wg sync.WaitGroup
	wg.Add(2)
	cluster1ExitError := make(chan error, 1)
	cluster2ExitError := make(chan error, 1)

	// unpeer cluster 1
	go func() {
		defer wg.Done()
		cluster1ExitError <- cluster1.RemoveCluster(ctx, cluster2.NetConfig.ClusterID)
	}()

	// unpeer cluster 2
	go func() {
		defer wg.Done()
		cluster2ExitError <- cluster2.RemoveCluster(ctx, cluster1.NetConfig.ClusterID)
	}()

	wg.Wait()
	errorCluster1 := <-cluster1ExitError
	errorCluster2 := <-cluster2ExitError
	if errorCluster1 != nil {
		fmt.Printf("%s -> unable to remove cluster with id {%s}: %s\n", cluster1.NetConfig.ClusterID, cluster2.NetConfig.ClusterID, err)
	}

	if errorCluster2 != nil {
		fmt.Printf("%s -> unable to remove cluster with  id {%s}: %s\n", cluster2.NetConfig.ClusterID, cluster1.NetConfig.ClusterID, err)
	}

	if errorCluster1 != nil || errorCluster2 != nil {
		return fmt.Errorf("unable to disconnect clusters")
	}

	wg.Add(2)

	// unpeer cluster 1
	go func() {
		defer wg.Done()
		cluster1ExitError <- cluster1.RemoveTEP(ctx, cluster2.NetConfig.ClusterID)
	}()

	// unpeer cluster 2
	go func() {
		defer wg.Done()
		cluster2ExitError <- cluster2.RemoveTEP(ctx, cluster1.NetConfig.ClusterID)
	}()

	wg.Wait()
	errorCluster1 = <-cluster1ExitError
	errorCluster2 = <-cluster2ExitError
	if errorCluster1 != nil {
		fmt.Printf("%s -> unable to remove TEP for cluster with id {%s}: %s\n", cluster1.NetConfig.ClusterID, cluster2.NetConfig.ClusterID, err)
	}

	if errorCluster2 != nil {
		fmt.Printf("%s -> unable to remove TEP for cluster with id {%s}: %s\n", cluster2.NetConfig.ClusterID, cluster1.NetConfig.ClusterID, err)
	}

	if errorCluster1 != nil || errorCluster2 != nil {
		return fmt.Errorf("unable to disconnect clusters")
	}

	return nil
}
