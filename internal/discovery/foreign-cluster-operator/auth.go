package foreign_cluster_operator

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	discoveryv1alpha1 "github.com/liqotech/liqo/apis/discovery/v1alpha1"
	"github.com/liqotech/liqo/internal/monitoring"
	"github.com/liqotech/liqo/pkg/auth"
	"github.com/liqotech/liqo/pkg/crdClient"
	"github.com/liqotech/liqo/pkg/discovery"
	"github.com/liqotech/liqo/pkg/kubeconfig"
	"io/ioutil"
	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	client_scheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	"net/http"
	"strings"
)

// get a client to the remote cluster
// if the ForeignCluster has a reference to the secret's role, load the configurations from that secret
// else try to get a role from the remote cluster
//
// first of all, if the status is pending we can try to get a role with an empty token, if the remote cluster allows it
// the status will become accepted
//
// if our status is EmptyRefused, this means that the remote cluster refused out request with the empty token, so we will
// wait to have a token to ask for the role again
//
// while we are waiting for that secret this function will return no error, but an empty client
func (r *ForeignClusterReconciler) getRemoteClient(fc *discoveryv1alpha1.ForeignCluster, gv *schema.GroupVersion) (*crdClient.CRDClient, error) {
	if strings.HasPrefix(fc.Spec.AuthUrl, "fake://") {
		config := *r.ForeignConfig

		config.ContentConfig.GroupVersion = gv
		config.APIPath = "/apis"
		config.NegotiatedSerializer = client_scheme.Codecs.WithoutConversion()
		config.UserAgent = rest.DefaultKubernetesUserAgent()

		fc.Status.AuthStatus = discovery.AuthStatusAccepted

		return crdClient.NewFromConfig(&config)
	}

	if client, err := r.getIdentity(fc, gv); err == nil {
		return client, nil
	} else if !kerrors.IsNotFound(err) {
		klog.Error(err)
		return nil, err
	}

	if fc.Status.AuthStatus == discovery.AuthStatusAccepted {
		// TODO: handle this possibility
		// this can happen if the role was accepted but the local secret has been removed
		err := errors.New("auth status is accepted but there is no secret found")
		klog.Error(err)
		return nil, err
	}

	monitoring.GetDiscoveryProcessMonitoring().EventRegister(monitoring.Discovery, monitoring.RetrieveRemoteIdentity, monitoring.Start)

	// not existing role
	if fc.Status.AuthStatus == discovery.AuthStatusPending || (fc.Status.AuthStatus == discovery.AuthStatusEmptyRefused && r.getAuthToken(fc) != "") {
		kubeconfigStr, err := r.askRemoteIdentity(fc)
		if err != nil {
			klog.Error(err)
			return nil, err
		}
		if kubeconfigStr == "" {
			return nil, nil
		}
		_, err = kubeconfig.CreateSecret(r.crdClient.Client(), r.Namespace, kubeconfigStr, map[string]string{
			discovery.ClusterIdLabel:      fc.Spec.ClusterIdentity.ClusterID,
			discovery.RemoteIdentityLabel: "",
		})
		if err != nil {
			klog.Error(err)
			return nil, err
		}

		monitoring.GetDiscoveryProcessMonitoring().EventRegister(monitoring.Discovery, monitoring.RetrieveRemoteIdentity, monitoring.End)
		monitoring.GetDiscoveryProcessMonitoring().Complete(monitoring.DiscoveryRetrieveIdentity)

		return nil, nil
	}

	klog.V(4).Info("no available identity")
	return nil, nil
}

// load remote identity from a secret
func (r *ForeignClusterReconciler) getIdentity(fc *discoveryv1alpha1.ForeignCluster, gv *schema.GroupVersion) (*crdClient.CRDClient, error) {
	secrets, err := r.crdClient.Client().CoreV1().Secrets(r.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: strings.Join([]string{
			strings.Join([]string{discovery.ClusterIdLabel, fc.Spec.ClusterIdentity.ClusterID}, "="),
			discovery.RemoteIdentityLabel,
		}, ","),
	})
	if err != nil {
		klog.Error(err)
		return nil, err
	}

	if len(secrets.Items) == 0 {
		err = kerrors.NewNotFound(schema.GroupResource{
			Group:    v1.GroupName,
			Resource: string(v1.ResourceSecrets),
		}, fmt.Sprintf("%s %s", discovery.RemoteIdentityLabel, fc.Spec.ClusterIdentity.ClusterID))
		return nil, err
	}

	roleSecret := &secrets.Items[0]

	config, err := kubeconfig.LoadFromSecret(roleSecret)
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	config.ContentConfig.GroupVersion = gv
	config.APIPath = "/apis"
	config.NegotiatedSerializer = client_scheme.Codecs.WithoutConversion()
	config.UserAgent = rest.DefaultKubernetesUserAgent()

	fc.Status.AuthStatus = discovery.AuthStatusAccepted

	return crdClient.NewFromConfig(config)
}

// load the auth token form a labelled secret
func (r *ForeignClusterReconciler) getAuthToken(fc *discoveryv1alpha1.ForeignCluster) string {
	tokenSecrets, err := r.crdClient.Client().CoreV1().Secrets(r.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: strings.Join(
			[]string{
				strings.Join([]string{discovery.ClusterIdLabel, fc.Spec.ClusterIdentity.ClusterID}, "="),
				discovery.AuthTokenLabel,
			},
			",",
		),
	})
	if err != nil {
		klog.Error(err)
		return ""
	}

	for _, tokenSecret := range tokenSecrets.Items {
		if token, found := tokenSecret.Data["token"]; found {
			return string(token)
		}
	}
	return ""
}

// send HTTP request to get the identity from the remote cluster
func (r *ForeignClusterReconciler) askRemoteIdentity(fc *discoveryv1alpha1.ForeignCluster) (string, error) {
	token := r.getAuthToken(fc)

	roleRequest := auth.IdentityRequest{
		ClusterID: r.clusterID.GetClusterID(),
		Token:     token,
	}
	jsonRequest, err := json.Marshal(roleRequest)
	if err != nil {
		klog.Error(err)
		return "", err
	}

	resp, err := sendRequest(fmt.Sprintf("%s/identity", fc.Spec.AuthUrl), bytes.NewBuffer(jsonRequest))
	if err != nil {
		klog.Error(err)
		return "", err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		klog.Error(err)
		return "", err
	}
	switch resp.StatusCode {
	case http.StatusCreated:
		fc.Status.AuthStatus = discovery.AuthStatusAccepted
		klog.Info("Identity Created")
		return string(body), nil
	case http.StatusForbidden:
		if token == "" {
			fc.Status.AuthStatus = discovery.AuthStatusEmptyRefused
		} else {
			fc.Status.AuthStatus = discovery.AuthStatusRefused
		}
		klog.Info(string(body))
		return "", nil
	default:
		klog.Info(string(body))
		return "", errors.New(string(body))
	}
}

func sendRequest(url string, payload *bytes.Buffer) (*http.Response, error) {
	// disable TLS CA check for untrusted remote clusters
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}
	return client.Post(url, "text/plain", payload)
}
