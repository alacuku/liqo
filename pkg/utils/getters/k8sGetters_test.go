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

package getters_test

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	netv1alpha1 "github.com/liqotech/liqo/apis/net/v1alpha1"
	liqoconst "github.com/liqotech/liqo/pkg/consts"
	"github.com/liqotech/liqo/pkg/liqonet/tunnel/wireguard"
	"github.com/liqotech/liqo/pkg/utils/getters"
	liqolabels "github.com/liqotech/liqo/pkg/utils/labels"
)

func TestPod(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Getters")
}

//nolint:dupl //false positive as duplicated code.
var _ = Describe("Getters", func() {
	var (
		scheme    = runtime.NewScheme()
		ctx       context.Context
		namespace = "test"
	)

	utilruntime.Must(netv1alpha1.AddToScheme(scheme))
	ctx = context.Background()

	Context("GetIPAMStorageByLabel function", func() {
		selector, err := metav1.LabelSelectorAsSelector(&liqolabels.IPAMStorageLabelSelector)
		Expect(err).To(Succeed())
		Expect(selector).NotTo(BeNil())

		foundClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&netv1alpha1.IpamStorage{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						liqoconst.IpamStorageResourceLabelKey: liqoconst.IpamStorageResourceLabelValue,
					},
				},
			},
			&netv1alpha1.IpamStorage{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test2",
					Labels: map[string]string{
						liqoconst.IpamStorageResourceLabelKey: "wrong value",
					},
				}},
		).Build()

		notFoundClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&netv1alpha1.IpamStorage{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			}).Build()

		multipleFoundClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&netv1alpha1.IpamStorage{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test1",
					Labels: map[string]string{
						liqoconst.IpamStorageResourceLabelKey: liqoconst.IpamStorageResourceLabelValue,
					},
				},
			},
			&netv1alpha1.IpamStorage{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test2",
					Labels: map[string]string{
						liqoconst.IpamStorageResourceLabelKey: liqoconst.IpamStorageResourceLabelValue,
					},
				},
			}).Build()

		When("only one ipam storage instance exists", func() {
			It("should succeed", func() {
				i, err := getters.GetIPAMStorageByLabel(ctx, foundClient, selector)
				Expect(err).To(Succeed())
				Expect(i).NotTo(BeNil())
			})
		})

		When("no ipam storage instance exists", func() {
			It("should error", func() {
				i, err := getters.GetIPAMStorageByLabel(ctx, notFoundClient, selector)
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("no ipamstorage found for the given selector %s", selector.String())))
				Expect(i).To(BeNil())
			})
		})

		When("multiple ipam storage instances exist", func() {
			It("should error", func() {
				i, err := getters.GetIPAMStorageByLabel(ctx, multipleFoundClient, selector)
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("multiple resources of type %s found, when only one was expected", netv1alpha1.IpamGroupResource)))
				Expect(i).To(BeNil())
			})
		})
	})

	Context("GetServiceByLabel function", func() {
		foundClient := fake2.NewSimpleClientset(
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "servicetest",
					Namespace: namespace,
					Labels: map[string]string{
						liqoconst.GatewayServiceLabelKey: liqoconst.GatewayServiceLabelValue,
					},
				},
			},
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "servicetest1",
					Namespace: namespace,
					Labels: map[string]string{
						liqoconst.GatewayServiceLabelKey: "wrong value",
					},
				},
			})

		notFoundClient := fake2.NewSimpleClientset(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: namespace,
			},
		})

		multipleFoundClient := fake2.NewSimpleClientset(
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: namespace,
					Labels: map[string]string{
						liqoconst.GatewayServiceLabelKey: liqoconst.GatewayServiceLabelValue,
					},
				},
			},
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test1",
					Namespace: namespace,
					Labels: map[string]string{
						liqoconst.GatewayServiceLabelKey: liqoconst.GatewayServiceLabelValue,
					},
				},
			})

		When("only one service instance exists", func() {
			It("should succeed", func() {
				i, err := getters.GetServiceByLabel(ctx, foundClient, namespace, &liqolabels.GatewayServiceLabelSelector)
				Expect(err).To(Succeed())
				Expect(i).NotTo(BeNil())
			})
		})

		When("no service instance exists", func() {
			It("should error", func() {
				i, err := getters.GetServiceByLabel(ctx, notFoundClient, namespace, &liqolabels.GatewayServiceLabelSelector)
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("no service found for the given selector %s in namespace %s",
					metav1.FormatLabelSelector(&liqolabels.GatewayServiceLabelSelector), namespace)))
				Expect(i).To(BeNil())
			})
		})

		When("multiple service instances exist", func() {
			It("should error", func() {
				i, err := getters.GetServiceByLabel(ctx, multipleFoundClient, namespace, &liqolabels.GatewayServiceLabelSelector)
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("multiple resources of type services found in namespace %s, when only one was expected", namespace)))
				Expect(i).To(BeNil())
			})
		})
	})

	Context("GetSecretByLabel function", func() {
		foundClient := fake2.NewSimpleClientset(
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: namespace,
					Labels: map[string]string{
						wireguard.KeysLabel: wireguard.DriverName,
					},
				},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test1",
					Namespace: namespace,
					Labels: map[string]string{
						wireguard.KeysLabel: "wrongValue"},
				},
			})

		notFoundClient := fake2.NewSimpleClientset(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: namespace,
			},
		})

		multipleFoundClient := fake2.NewSimpleClientset(
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: namespace,
					Labels: map[string]string{
						wireguard.KeysLabel: wireguard.DriverName},
				},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test1",
					Namespace: namespace,
					Labels: map[string]string{
						wireguard.KeysLabel: wireguard.DriverName},
				},
			})

		When("only one secret instance exists", func() {
			It("should succeed", func() {
				i, err := getters.GetSecretByLabel(ctx, foundClient, namespace, &liqolabels.WireGuardSecretLabelSelector)
				Expect(err).To(Succeed())
				Expect(i).NotTo(BeNil())
			})
		})

		When("no secret instance exists", func() {
			It("should error", func() {
				i, err := getters.GetSecretByLabel(ctx, notFoundClient, namespace, &liqolabels.WireGuardSecretLabelSelector)
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("no secret found for the given selector %s in namespace %s",
					metav1.FormatLabelSelector(&liqolabels.WireGuardSecretLabelSelector), namespace)))
				Expect(i).To(BeNil())
			})
		})

		When("multiple secrets instances exist", func() {
			It("should error", func() {
				i, err := getters.GetSecretByLabel(ctx, multipleFoundClient, namespace, &liqolabels.WireGuardSecretLabelSelector)
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("multiple resources of type secrets found in namespace %s, when only one was expected", namespace)))
				Expect(i).To(BeNil())
			})
		})
	})

	Context("GetConfigMapByLabel function", func() {
		foundClient := fake2.NewSimpleClientset(
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: namespace,
					Labels: map[string]string{
						liqoconst.ClusterIDConfigMapNameLabelKey: liqoconst.ClusterIDConfigMapNameLabelValue},
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test1",
					Namespace: namespace,
					Labels: map[string]string{
						liqoconst.ClusterIDConfigMapNameLabelKey: "wrongValue"},
				},
			})

		notFoundClient := fake2.NewSimpleClientset(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: namespace,
			},
		})

		multipleFoundClient := fake2.NewSimpleClientset(
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: namespace,
					Labels: map[string]string{
						liqoconst.ClusterIDConfigMapNameLabelKey: liqoconst.ClusterIDConfigMapNameLabelValue},
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test1",
					Namespace: namespace,
					Labels: map[string]string{
						liqoconst.ClusterIDConfigMapNameLabelKey: liqoconst.ClusterIDConfigMapNameLabelValue},
				},
			})

		When("only one configmap instance exists", func() {
			It("should succeed", func() {
				i, err := getters.GetConfigMapByLabel(ctx, foundClient, namespace, &liqolabels.ClusterIDConfigMapLabelSelector)
				Expect(err).To(Succeed())
				Expect(i).NotTo(BeNil())
			})
		})

		When("no configmap instance exists", func() {
			It("should error", func() {
				i, err := getters.GetConfigMapByLabel(ctx, notFoundClient, namespace, &liqolabels.ClusterIDConfigMapLabelSelector)
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("no configmap found for the given selector %s in namespace %s",
					metav1.FormatLabelSelector(&liqolabels.ClusterIDConfigMapLabelSelector), namespace)))
				Expect(i).To(BeNil())
			})
		})

		When("multiple configmap instances exist", func() {
			It("should error", func() {
				i, err := getters.GetConfigMapByLabel(ctx, multipleFoundClient, namespace, &liqolabels.ClusterIDConfigMapLabelSelector)
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("multiple resources of type configmaps found in namespace %s, when only one was expected", namespace)))
				Expect(i).To(BeNil())
			})
		})
	})
})
