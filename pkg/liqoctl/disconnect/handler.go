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
	"github.com/liqotech/liqo/pkg/liqoctl/common"
	"github.com/liqotech/liqo/pkg/liqoctl/connect"
	"golang.org/x/net/context"
	"os"
	"sync"
)

// Args flags of the connect command.
type Args struct {
	Cluster1Namespace  string
	Cluster2Namespace  string
	Cluster1Kubeconfig string
	Cluster2Kubeconfig string
}

// Handler implements the logic of the disconnect command.
func (a *Args) Handler(ctx context.Context) error {
	// Check that the kubeconfigs are different.
	if a.Cluster1Kubeconfig == a.Cluster2Kubeconfig {
		common.ErrorPrinter.Printf("kubeconfig1 and kubeconfig2 has to be different, current value: %s", a.Cluster2Kubeconfig)
		return fmt.Errorf("kubeconfig1 and kubeconfig2 has to be different, current value: %s", a.Cluster2Kubeconfig)
	}

	cluster1, err := connect.NewCluster(a.Cluster1Kubeconfig, a.Cluster1Namespace, connect.Cluster1Name, connect.Cluster1Color)
	if err != nil {
		return err
	}
	if err := cluster1.Init(); err != nil {
		return err
	}
	defer cluster1.Stop()

	cluster2, err := connect.NewCluster(a.Cluster2Kubeconfig, a.Cluster2Namespace, connect.Cluster2Name, connect.Cluster2Color)
	if err != nil {
		return err
	}

	if err := cluster2.Init(); err != nil {
		return err
	}
	defer cluster2.Stop()

	// unpeer cluster 1
	var cluster1Error, cluster2Error error
	cluster1Chan := make(chan error, 1)
	cluster2Chan := make(chan error, 1)
	wg := sync.WaitGroup{}
	wg.Add(2)
	writer, stopCh := common.ConcurrentSpinner(fmt.Sprintf("(%s)", connect.Cluster1Name), fmt.Sprintf("(%s)", connect.Cluster2Name))

	cluster1.Printer.Spinner.Writer = writer
	cluster2.Printer.Spinner.Writer = writer

	go func() {
		defer wg.Done()
		cluster1Chan <-cluster1.RemoveCluster(ctx, cluster2.NetConfig.ClusterID)
	}()

	go func() {
		defer wg.Done()
		cluster2Chan <-cluster2.RemoveCluster(ctx, cluster1.NetConfig.ClusterID)
	}()
	wg.Wait()
	cluster1Error = <- cluster1Chan
	cluster2Error = <- cluster2Chan
	if cluster1Error != nil{
		return err
	}
	if cluster2Error != nil{
		return err
	}
	stopCh <- true
	fmt.Fprintf(writer, "")



	wg.Add(2)
	writer, stopCh = common.ConcurrentSpinner(fmt.Sprintf("(%s)", connect.Cluster1Name), fmt.Sprintf("(%s)", connect.Cluster2Name))

	cluster1.Printer.Spinner.Writer = writer
	cluster2.Printer.Spinner.Writer = writer
	go func() {
		defer wg.Done()
		cluster1Chan <- cluster1.RemoveTEP(ctx, cluster2.NetConfig.ClusterID)
	}()

	go func() {
		defer wg.Done()
		cluster2Chan <- cluster2.RemoveTEP(ctx, cluster1.NetConfig.ClusterID)
	}()

	wg.Wait()
	cluster1Error = <- cluster1Chan
	cluster2Error = <- cluster2Chan
	if cluster1Error != nil{
		return err
	}
	if cluster2Error != nil{
		return err
	}

	cluster1.Printer.Spinner.Writer = os.Stdout
	cluster2.Printer.Spinner.Writer = os.Stdout

	return nil
}
