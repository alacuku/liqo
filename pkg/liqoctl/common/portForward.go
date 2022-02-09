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
	"net/http"
	"net/url"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	k8s "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForwardOptions contains all the options in order to port-forward a pod's port.
type PortForwardOptions struct {
	Namespace     string
	Selector      *metav1.LabelSelector
	Config        *restclient.Config
	Client        k8s.Interface
	PortForwarder portForwarder
	RemotePort    int
	LocalPort     int
	Ports         []string
	StopChannel   chan struct{}
	ReadyChannel  chan struct{}
}

type portForwarder interface {
	ForwardPorts(method string, url *url.URL, opts PortForwardOptions) error
}

type DefaultPortForwarder struct {
	genericclioptions.IOStreams
}

func (f *DefaultPortForwarder) ForwardPorts(method string, url *url.URL, opt PortForwardOptions) error {
	errChan := make(chan error, 1)

	transport, upgrader, err := spdy.RoundTripperFor(opt.Config)
	if err != nil {
		return err
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, method, url)

	pf, err := portforward.New(dialer, opt.Ports, opt.StopChannel, opt.ReadyChannel, f.Out, f.ErrOut)
	if err != nil {
		return fmt.Errorf("unable to port forward into pod %s: %w", url.String(), err)
	}

	go func() {
		errChan <- pf.ForwardPorts()
	}()

	select {
	case err = <-errChan:
		if err != nil {
			return fmt.Errorf("an error occurred while port forwarding into pod %s: %w", url.String(), err)
		}
	case <-opt.ReadyChannel:
		break
	}

	return nil
}

func (o *PortForwardOptions) RunPortForward(ctx context.Context) error {
	var err error
	// Get the local port used to forward the pod's port.
	o.LocalPort, err = getFreePort()
	if err != nil {
		return fmt.Errorf("unable to get a local port: %w", err)
	}

	podURL, err := o.getPodURL(ctx)
	if err != nil {
		return err
	}

	o.Ports = []string{fmt.Sprintf("%d:%d", o.LocalPort, o.RemotePort)}

	return o.PortForwarder.ForwardPorts(http.MethodPost, podURL, *o)
}

func (o *PortForwardOptions) StopPortForward() {
	o.StopChannel <- struct{}{}
}

//todo: write a getter for the pod object.
func (o *PortForwardOptions) getPodURL(ctx context.Context) (*url.URL, error) {
	labels := metav1.FormatLabelSelector(o.Selector)
	pods, err := o.Client.CoreV1().Pods(o.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels,
		FieldSelector: fields.OneTermEqualSelector("status.phase", string(v1.PodRunning)).String(),
	})

	if err != nil {
		return nil, fmt.Errorf("unable to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no running pods found for selector: %s", labels)
	}

	if len(pods.Items) != 1 {
		return nil, fmt.Errorf("multiple pods found for selector: %s", labels)
	}

	return o.Client.CoreV1().RESTClient().Post().Resource("pods").Namespace(pods.Items[0].Namespace).Name(pods.Items[0].Name).SubResource("portforward").URL(), nil
}
