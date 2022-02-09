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

package utils_test

import (
	"fmt"
	"net"
	"strconv"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	netv1alpha1 "github.com/liqotech/liqo/apis/net/v1alpha1"
	liqoconst "github.com/liqotech/liqo/pkg/consts"
	"github.com/liqotech/liqo/pkg/liqonet/tunnel/wireguard"
	"github.com/liqotech/liqo/pkg/liqonet/utils"
)

const (
	invalidValue      = "invalidValue"
	CIDRAddressNetErr = "CIDR address"
	labelKey          = "net.liqo.io/LabelKey"
	labelValue        = "LabelValue"
	annotationKey     = "net.liqo.io/AnnotationKey"
	annotationValue   = "AnnotationValue"
	interfaceName     = "dummy-link"
)

var (
	// corev1.Pod impements the client.Object interface.
	testPod *corev1.Pod
)

var _ = Describe("Liqonet", func() {
	JustBeforeEach(func() {
		testPod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					labelKey: labelValue,
				},
				Annotations: map[string]string{
					annotationKey: annotationValue,
				},
			}}
	})

	DescribeTable("MapIPToNetwork",
		func(oldIp, newPodCidr, expectedIP string, expectedErr string) {
			ip, err := utils.MapIPToNetwork(oldIp, newPodCidr)
			if expectedErr != "" {
				Expect(err.Error()).To(Equal(expectedErr))
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(ip).To(Equal(expectedIP))
		},
		Entry("Mapping 10.2.1.3 to 10.0.4.0/24", "10.0.4.0/24", "10.2.1.3", "10.0.4.3", ""),
		Entry("Mapping 10.2.1.128 to 10.0.4.0/24", "10.0.4.0/24", "10.2.1.128", "10.0.4.128", ""),
		Entry("Mapping 10.2.1.1 to 10.0.4.0/24", "10.0.4.0/24", "10.2.1.1", "10.0.4.1", ""),
		Entry("Mapping 10.2.127.128 to 10.0.128.0/23", "10.0.128.0/23", "10.2.127.128", "10.0.129.128", ""),
		Entry("Mapping 10.2.128.128 to 10.0.126.0/23", "10.0.127.0/23", "10.2.128.128", "10.0.127.128", ""),
		Entry("Mapping 10.2.128.128 to 10.0.126.0/25", "10.0.126.0/25", "10.2.128.128", "10.0.126.0", ""),
		Entry("Using an invalid newPodCidr", "10.0..0/25", "10.2.128.128", "", "invalid CIDR address: 10.0..0/25"),
		Entry("Using an invalid oldIp", "10.0.0.0/25", "10.2...128", "", "cannot parse oldIP"),
	)

	DescribeTable("GetFirstIP",
		func(network, expectedIP string, expectedErr *net.ParseError) {
			ip, err := utils.GetFirstIP(network)
			if expectedErr != nil {
				Expect(err).To(MatchError(expectedErr))
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(ip).To(Equal(expectedIP))
		},
		Entry("Passing an invalid network", invalidValue, "", &net.ParseError{Type: CIDRAddressNetErr, Text: invalidValue}),
		Entry("Passing an empty network", "", "", &net.ParseError{Type: CIDRAddressNetErr, Text: ""}),
		Entry("Passing an IP", "10.0.0.0", "", &net.ParseError{Type: CIDRAddressNetErr, Text: "10.0.0.0"}),
		Entry("Getting first IP of 10.0.0.0/8", "10.0.0.0/8", "10.0.0.0", nil),
		Entry("Getting first IP of 192.168.0.0/16", "192.168.0.0/16", "192.168.0.0", nil),
	)

	Describe("testing getOverlayIP function", func() {
		Context("when input parameter is correct", func() {
			It("should return a valid ip", func() {
				Expect(utils.GetOverlayIP("10.200.1.1")).Should(Equal("240.200.1.1"))
			})
		})

		Context("when input parameter is not correct", func() {
			It("should return an empty string", func() {
				Expect(utils.GetOverlayIP("10.200.")).Should(Equal(""))
			})
		})
	})

	Describe("testing AddAnnotationToObj function", func() {
		Context("when annotations map is nil", func() {
			It("should create the map and return true", func() {
				testPod.Annotations = nil
				ok := utils.AddAnnotationToObj(testPod, annotationKey, annotationValue)
				Expect(ok).Should(BeTrue())
				Expect(len(testPod.GetAnnotations())).Should(BeNumerically("==", 1))
			})
		})

		Context("when annotation already exists", func() {
			It("annotation is the same, should return false", func() {
				ok := utils.AddAnnotationToObj(testPod, annotationKey, annotationValue)
				Expect(ok).Should(BeFalse())
				Expect(len(testPod.GetAnnotations())).Should(BeNumerically("==", 1))
			})

			It("annotation value is outdated", func() {
				const newValue = "differentValue"
				ok := utils.AddAnnotationToObj(testPod, annotationKey, newValue)
				Expect(ok).Should(BeTrue())
				Expect(len(testPod.GetAnnotations())).Should(BeNumerically("==", 1))
				value, ok := testPod.GetAnnotations()[annotationKey]
				Expect(value).Should(Equal(newValue))
				Expect(ok).Should(BeTrue())
			})
		})

		Context("when annotation with given key does not exist", func() {
			It("should return true", func() {
				const newKey = "newTestingKey"
				ok := utils.AddAnnotationToObj(testPod, newKey, annotationValue)
				Expect(ok).Should(BeTrue())
				Expect(len(testPod.GetAnnotations())).Should(BeNumerically("==", 2))
				value, ok := testPod.GetAnnotations()[annotationKey]
				Expect(value).Should(Equal(annotationValue))
				Expect(ok).Should(BeTrue())
			})
		})
	})

	Describe("testing GetAnnotationValueFromObj function", func() {
		Context("when annotations map is nil", func() {
			It("should return an empty string", func() {
				testPod.Annotations = nil
				value := utils.GetAnnotationValueFromObj(testPod, annotationKey)
				Expect(value).Should(Equal(""))
			})
		})

		Context("annotation with the given key exists", func() {
			It("should return the correct value", func() {
				value := utils.GetAnnotationValueFromObj(testPod, annotationKey)
				Expect(value).Should(Equal(annotationValue))
			})
		})

		Context("annotation with the given key does not exist", func() {
			It("should return an empty string", func() {
				value := utils.GetAnnotationValueFromObj(testPod, "notExistinKey")
				Expect(value).Should(Equal(""))
			})
		})
	})

	Describe("testing AddLabelToObj function", func() {
		Context("when label map is nil", func() {
			It("should create the map and return true", func() {
				testPod.Labels = nil
				ok := utils.AddLabelToObj(testPod, labelKey, labelValue)
				Expect(ok).Should(BeTrue())
				Expect(len(testPod.GetLabels())).Should(BeNumerically("==", 1))
			})
		})

		Context("when label already exists", func() {
			It("label is the same, should return false", func() {
				ok := utils.AddLabelToObj(testPod, labelKey, labelValue)
				Expect(ok).Should(BeFalse())
				Expect(len(testPod.GetLabels())).Should(BeNumerically("==", 1))
			})

			It("label value is outdated", func() {
				newValue := "differentValue"
				ok := utils.AddLabelToObj(testPod, labelKey, newValue)
				Expect(ok).Should(BeTrue())
				Expect(len(testPod.GetAnnotations())).Should(BeNumerically("==", 1))
				value, ok := testPod.GetLabels()[labelKey]
				Expect(value).Should(Equal(newValue))
				Expect(ok).Should(BeTrue())
			})
		})

		Context("when label with given key does not exist", func() {
			It("should return true", func() {
				newKey := "newTestingKey"
				ok := utils.AddLabelToObj(testPod, newKey, labelValue)
				Expect(ok).Should(BeTrue())
				Expect(len(testPod.GetLabels())).Should(BeNumerically("==", 2))
				value, ok := testPod.GetLabels()[newKey]
				Expect(value).Should(Equal(labelValue))
				Expect(ok).Should(BeTrue())
			})
		})
	})

	Describe("testing GetLabelValueFromObj function", func() {
		Context("when label map is nil", func() {
			It("should return an empty string", func() {
				testPod.Labels = nil
				value := utils.GetLabelValueFromObj(testPod, labelKey)
				Expect(value).Should(Equal(""))
			})
		})

		Context("label with the given key exists", func() {
			It("should return the correct value", func() {
				value := utils.GetLabelValueFromObj(testPod, labelKey)
				Expect(value).Should(Equal(labelValue))
			})
		})

		Context("label with the given key does not exist", func() {
			It("should return an empty string", func() {
				value := utils.GetLabelValueFromObj(testPod, "nonExistingKey")
				Expect(value).Should(Equal(""))
			})
		})
	})

	Describe("testing DeleteIfaceByName function", func() {
		Context("when network interface exists", func() {
			BeforeEach(func() {
				// Create dummy link.
				err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: interfaceName}})
				Expect(err).ShouldNot(HaveOccurred())
			})

			It("should return nil", func() {
				err := utils.DeleteIFaceByName(interfaceName)
				Expect(err).Should(BeNil())
				_, err = netlink.LinkByName(interfaceName)
				Expect(err).Should(MatchError("Link not found"))
			})
		})

		Context("when network interface does not exist", func() {
			It("should return nil", func() {
				err := utils.DeleteIFaceByName("not-existing")
				Expect(err).Should(BeNil())
			})
		})
	})

	Describe("testing GetNetworkConfiguration function", func() {

		var (
			ipamStorage  netv1alpha1.IpamStorage
			resNets      = []string{"10.1.0.0/16", "192.168.0.0/16"}
			podCIDR      = "10.200.0.0/16"
			serviceCIDR  = "10.150.2.0/24"
			externalCIDR = "10.201.0.0/16"
		)

		BeforeEach(func() {
			ipamStorage = netv1alpha1.IpamStorage{
				Spec: netv1alpha1.IpamSpec{
					ReservedSubnets: resNets,
					ExternalCIDR:    externalCIDR,
					PodCIDR:         podCIDR,
					ServiceCIDR:     serviceCIDR,
				},
			}
		})

		Context("when podCIDR has not been set", func() {
			It("should return error", func() {
				ipamStorage.Spec.PodCIDR = ""
				config, err := utils.GetNetworkConfiguration(&ipamStorage)
				Expect(config).To(BeNil())
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("podCIDR is not set")))
			})
		})

		Context("when externalCIDR has not been set", func() {
			It("should return error", func() {
				ipamStorage.Spec.ExternalCIDR = ""
				config, err := utils.GetNetworkConfiguration(&ipamStorage)
				Expect(config).To(BeNil())
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("externalCIDR is not set")))
			})
		})

		Context("when serviceCIDR has not been set", func() {
			It("should return error", func() {
				ipamStorage.Spec.ServiceCIDR = ""
				config, err := utils.GetNetworkConfiguration(&ipamStorage)
				Expect(config).To(BeNil())
				Expect(err).NotTo(Succeed())
				Expect(err).To(MatchError(fmt.Errorf("serviceCIDR is not set")))
			})
		})

		Context("when all fields has been set", func() {
			It("should return configuration and nil", func() {
				config, err := utils.GetNetworkConfiguration(&ipamStorage)
				Expect(err).To(Succeed())
				Expect(config).NotTo(BeNil())
				Expect(config.ServiceCIDR).To(Equal(serviceCIDR))
				Expect(config.ExternalCIDR).To(Equal(externalCIDR))
				Expect(config.PodCIDR).To(Equal(podCIDR))
				Expect(config.ReservedSubnets).To(Equal(resNets))
			})
		})
	})

	Describe("testing retrieval of WireGuard endpoint from service object", func() {

		var (
			service    *corev1.Service
			nodeIPAddr = "192.168.0.162"
			port       = corev1.ServicePort{
				Name:     "wireguard",
				Protocol: corev1.ProtocolUDP,
				Port:     5871,
				NodePort: 32444,
			}
			loadBalancerIP = "10.0.0.1"

			loadBalancerHost = "testingWGEndpoint"
		)

		BeforeEach(func() {
			service = &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						liqoconst.GatewayServiceLabelKey: liqoconst.GatewayServiceLabelValue,
					},
					Annotations: map[string]string{
						liqoconst.GatewayServiceAnnotationKey: nodeIPAddr,
					},
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{port},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{
								IP:       loadBalancerIP,
								Hostname: loadBalancerHost,
							},
						},
					},
				},
			}
		})

		Context("when service is of type NodePort", func() {
			Context("when ip of the node has not been added as annotation to the service", func() {
				It("should return error", func() {
					delete(service.Annotations, liqoconst.GatewayServiceAnnotationKey)
					ip, port, err := utils.RetrieveWGEPFromNodePort(service, liqoconst.GatewayServiceAnnotationKey, wireguard.DriverName)
					Expect(err).NotTo(Succeed())
					Expect(ip).To(BeEmpty())
					Expect(port).To(BeEmpty())
				})
			})

			Context("when port with given name does not exist", func() {
				It("should return error", func() {
					service.Spec.Ports = nil
					_, _, err := utils.RetrieveWGEPFromNodePort(service, liqoconst.GatewayServiceAnnotationKey, wireguard.DriverName)
					Expect(err).NotTo(Succeed())
				})
			})

			Context("when node port has no been set", func() {
				It("should return error", func() {
					service.Spec.Ports[0].NodePort = 0
					_, _, err := utils.RetrieveWGEPFromNodePort(service, liqoconst.GatewayServiceAnnotationKey, wireguard.DriverName)
					Expect(err).NotTo(Succeed())
				})
			})

			Context("service is ready", func() {
				It("should return nil", func() {
					ip, epPort, err := utils.RetrieveWGEPFromNodePort(service, liqoconst.GatewayServiceAnnotationKey, wireguard.DriverName)
					Expect(err).To(Succeed())
					Expect(ip).To(Equal(nodeIPAddr))
					Expect(epPort).To(Equal(strconv.FormatInt(int64(port.NodePort), 10)))
				})
			})
		})

		Context("when service is of type LoadBalancer", func() {
			Context("when the LoadBalancer IP has not been set", func() {
				It("should return error", func() {
					service.Status.LoadBalancer.Ingress = nil
					ip, port, err := utils.RetrieveWGEPFromLoadBalancer(service, wireguard.DriverName)
					Expect(err).NotTo(Succeed())
					Expect(ip).To(BeEmpty())
					Expect(port).To(BeEmpty())
				})
			})

			Context("when port with given name does not exist", func() {
				It("should return err", func() {
					service.Spec.Ports = nil
					_, _, err := utils.RetrieveWGEPFromLoadBalancer(service, wireguard.DriverName)
					Expect(err).NotTo(Succeed())
				})
			})

			Context("when only the ip address has been set", func() {
				It("should return nil", func() {
					ip, epPort, err := utils.RetrieveWGEPFromLoadBalancer(service, wireguard.DriverName)
					Expect(err).To(Succeed())
					Expect(ip).To(Equal(loadBalancerIP))
					Expect(epPort).To(Equal(strconv.FormatInt(int64(port.Port), 10)))
				})
			})

			Context("when only the hostname has been set", func() {
				It("should return nil", func() {
					service.Status.LoadBalancer.Ingress[0].IP = ""
					ip, epPort, err := utils.RetrieveWGEPFromLoadBalancer(service, wireguard.DriverName)
					Expect(err).To(Succeed())
					Expect(ip).To(Equal(loadBalancerHost))
					Expect(epPort).To(Equal(strconv.FormatInt(int64(port.Port), 10)))
				})
			})
		})
	})

	Describe("testing retrieval of WireGuard public Key from secret object", func() {
		var (
			secret     *corev1.Secret
			correctKey = "cHVibGljLWtleS1vZi10aGUtY29ycmVjdC1sZW5ndGg="
			wrongKey   = "incorrect key"
		)

		BeforeEach(func() {
			secret = &corev1.Secret{
				Data: map[string][]byte{wireguard.PublicKey: []byte(correctKey)},
			}
		})

		Context("when key with given name does not exist", func() {
			It("should return nil", func() {
				delete(secret.Data, wireguard.PublicKey)
				_, err := utils.RetrieveWGPubKeyFromSecret(secret, wireguard.PublicKey)
				Expect(err).NotTo(Succeed())
			})
		})

		Context("when key is wrong format", func() {
			It("should return err", func() {
				secret.Data[wireguard.PublicKey] = []byte(wrongKey)
				_, err := utils.RetrieveWGPubKeyFromSecret(secret, wireguard.PublicKey)
				Expect(err).NotTo(Succeed())
			})
		})

		Context("when key exists", func() {
			It("should return nil", func() {
				key, err := utils.RetrieveWGPubKeyFromSecret(secret, wireguard.PublicKey)
				Expect(err).To(Succeed())
				Expect(key.String()).To(Equal(correctKey))
			})
		})
	})
})
