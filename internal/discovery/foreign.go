package discovery

import (
	"errors"
	"github.com/liqotech/liqo/apis/discovery/v1alpha1"
	"github.com/liqotech/liqo/internal/monitoring"
	discoveryPkg "github.com/liqotech/liqo/pkg/discovery"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
	"strings"
)

// 1. checks if cluster ID is already known
// 2. if not exists, create it
// 3. else
//   3a. if IP is different set new IP and delete CA data
//   3b. else it is ok

func (discovery *DiscoveryCtrl) UpdateForeignLAN(data *discoveryData, trustMode discoveryPkg.TrustMode) {
	discoveryType := discoveryPkg.LanDiscovery
	if data.ClusterInfo.ClusterID == discovery.ClusterId.GetClusterID() {
		// is local cluster
		return
	}

	err := retry.OnError(
		retry.DefaultRetry,
		func(err error) bool {
			return k8serror.IsConflict(err) || k8serror.IsAlreadyExists(err)
		},
		func() error {
			return discovery.createOrUpdate(data, trustMode, nil, discoveryType, nil)
		})
	if err != nil {
		klog.Error(err)
	}
}

// update the list of known foreign clusters:
// for each cluster retrieved with DNS discovery, if it is not the local cluster, check if it is already known, if not
// create it. In both cases update the ForeignCluster TTL
// This function also sets an owner reference and a label to the ForeignCluster pointing to the SearchDomain CR
func (discovery *DiscoveryCtrl) UpdateForeignWAN(data []*AuthData, sd *v1alpha1.SearchDomain) []*v1alpha1.ForeignCluster {
	createdUpdatedForeign := []*v1alpha1.ForeignCluster{}
	discoveryType := discoveryPkg.WanDiscovery
	for _, authData := range data {
		clusterInfo, trustMode, err := discovery.getClusterInfo(authData)
		if err != nil {
			klog.Error(err)
			continue
		}

		if clusterInfo.ClusterID == discovery.ClusterId.GetClusterID() {
			// is local cluster
			continue
		}

		err = retry.OnError(
			retry.DefaultRetry,
			func(err error) bool {
				return k8serror.IsConflict(err) || k8serror.IsAlreadyExists(err)
			},
			func() error {
				return discovery.createOrUpdate(&discoveryData{
					AuthData:    authData,
					ClusterInfo: clusterInfo,
				}, trustMode, sd, discoveryType, &createdUpdatedForeign)
			})
		if err != nil {
			klog.Error(err)
			continue
		}
	}
	return createdUpdatedForeign
}

func (discovery *DiscoveryCtrl) createOrUpdate(data *discoveryData, trustMode discoveryPkg.TrustMode, sd *v1alpha1.SearchDomain, discoveryType discoveryPkg.DiscoveryType, createdUpdatedForeign *[]*v1alpha1.ForeignCluster) error {
	fc, err := discovery.GetForeignClusterByID(data.ClusterInfo.ClusterID)
	if k8serror.IsNotFound(err) {
		fc, err := discovery.createForeign(data, trustMode, sd, discoveryType)
		if err != nil {
			klog.Error(err)
			return err
		}
		klog.Infof("ForeignCluster %s created", data.ClusterInfo.ClusterID)
		if createdUpdatedForeign != nil {
			*createdUpdatedForeign = append(*createdUpdatedForeign, fc)
		}
		monitoring.GetDiscoveryProcessMonitoring().Complete(monitoring.DiscoveryCreateForeignCluster)
	} else if err == nil {
		var updated bool
		fc, updated, err = discovery.CheckUpdate(data, fc, discoveryType, sd)
		if err != nil {
			if !k8serror.IsConflict(err) {
				klog.Error(err)
			}
			return err
		}
		if updated {
			klog.Infof("ForeignCluster %s updated", data.ClusterInfo.ClusterID)
			if createdUpdatedForeign != nil {
				*createdUpdatedForeign = append(*createdUpdatedForeign, fc)
			}
			monitoring.GetDiscoveryProcessMonitoring().Complete(monitoring.DiscoveryUpdateForeignCluster)
		}
	} else {
		// unhandled errors
		klog.Error(err)
		return err
	}
	return nil
}

func (discovery *DiscoveryCtrl) createForeign(data *discoveryData, trustMode discoveryPkg.TrustMode, sd *v1alpha1.SearchDomain, discoveryType discoveryPkg.DiscoveryType) (*v1alpha1.ForeignCluster, error) {
	fc := &v1alpha1.ForeignCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: data.ClusterInfo.ClusterID,
			Labels: map[string]string{
				discoveryPkg.DiscoveryTypeLabel: string(discoveryType),
				discoveryPkg.ClusterIdLabel:     data.ClusterInfo.ClusterID,
			},
		},
		Spec: v1alpha1.ForeignClusterSpec{
			ClusterIdentity: v1alpha1.ClusterIdentity{
				ClusterID:   data.ClusterInfo.ClusterID,
				ClusterName: data.ClusterInfo.ClusterName,
			},
			Namespace:     data.ClusterInfo.GuestNamespace,
			DiscoveryType: discoveryType,
			AuthUrl:       data.AuthData.GetUrl(),
			TrustMode:     trustMode,
		},
	}
	if trustMode == discoveryPkg.TrustModeTrusted {
		fc.Spec.Join = discovery.Config.AutoJoin
	} else if trustMode == discoveryPkg.TrustModeUntrusted {
		fc.Spec.Join = discovery.Config.AutoJoinUntrusted
	}
	fc.LastUpdateNow()

	if sd != nil {
		fc.Spec.Join = sd.Spec.AutoJoin
		fc.ObjectMeta.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: "discovery.liqo.io/v1alpha1",
				Kind:       "SearchDomain",
				Name:       sd.Name,
				UID:        sd.UID,
			},
		}
		if fc.Labels == nil {
			fc.Labels = map[string]string{}
		}
		fc.Labels[discoveryPkg.SearchDomainLabel] = sd.Name
	}
	// set TTL
	fc.Status.Ttl = data.AuthData.ttl
	tmp, err := discovery.crdClient.Resource("foreignclusters").Create(fc, metav1.CreateOptions{})
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	fc, ok := tmp.(*v1alpha1.ForeignCluster)
	if !ok {
		return nil, errors.New("created object is not a ForeignCluster")
	}
	return fc, err
}

// indicates that the remote cluster changed location, we have to reload all our info about the remote cluster
func needsToDeleteRemoteResources(fc *v1alpha1.ForeignCluster, data *discoveryData) bool {
	return fc.Spec.Namespace != data.ClusterInfo.GuestNamespace
}

func (discovery *DiscoveryCtrl) CheckUpdate(data *discoveryData, fc *v1alpha1.ForeignCluster, discoveryType discoveryPkg.DiscoveryType, searchDomain *v1alpha1.SearchDomain) (fcUpdated *v1alpha1.ForeignCluster, updated bool, err error) {
	needsToReload := needsToDeleteRemoteResources(fc, data)
	higherPriority := fc.HasHigherPriority(discoveryType) // the remote cluster didn't move, but we discovered it with an higher priority discovery type
	if needsToReload || higherPriority {
		// something is changed in ForeignCluster specs, update it
		fc.Spec.Namespace = data.ClusterInfo.GuestNamespace
		fc.Spec.DiscoveryType = discoveryType
		if higherPriority && discoveryType == discoveryPkg.LanDiscovery {
			// if the cluster was previously discovered with IncomingPeering discovery type, set join flag accordingly to LanDiscovery sets and set TTL
			fc.Spec.Join = fc.Spec.TrustMode == discoveryPkg.TrustModeTrusted && discovery.Config.AutoJoin || fc.Spec.TrustMode == discoveryPkg.TrustModeUntrusted && discovery.Config.AutoJoinUntrusted
			fc.Status.Ttl = data.AuthData.ttl
		} else if searchDomain != nil && discoveryType == discoveryPkg.WanDiscovery {
			fc.Spec.Join = searchDomain.Spec.AutoJoin
			fc.Status.Ttl = data.AuthData.ttl
		}
		fc.LastUpdateNow()
		tmp, err := discovery.crdClient.Resource("foreignclusters").Update(fc.Name, fc, metav1.UpdateOptions{})
		if err != nil {
			klog.Error(err)
			return nil, false, err
		}
		klog.V(4).Infof("TTL updated for ForeignCluster %v", fc.Name)
		fc, ok := tmp.(*v1alpha1.ForeignCluster)
		if !ok {
			err = errors.New("retrieved object is not a ForeignCluster")
			klog.Error(err)
			return nil, false, err
		}
		if needsToReload && fc.Status.Outgoing.Advertisement != nil {
			// delete it only if the remote cluster moved

			// changed ip in peered cluster, delete advertisement and wait for its recreation
			// TODO: find more sophisticated logic to not remove all resources on remote cluster
			advName := fc.Status.Outgoing.Advertisement.Name
			fc.Status.Outgoing.Advertisement = nil
			// updating it before adv delete will avoid us to set to false join flag
			tmp, err = discovery.crdClient.Resource("foreignclusters").Update(fc.Name, fc, metav1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
				return nil, false, err
			}
			fc, ok = tmp.(*v1alpha1.ForeignCluster)
			if !ok {
				err = errors.New("retrieved object is not a ForeignCluster")
				klog.Error(err)
				return nil, false, err
			}
			err = discovery.advClient.Resource("advertisements").Delete(advName, metav1.DeleteOptions{})
			if err != nil {
				klog.Error(err)
				return nil, false, err
			}
		}
		return fc, true, nil
	} else {
		// update "lastUpdate" annotation
		fc.LastUpdateNow()
		tmp, err := discovery.crdClient.Resource("foreignclusters").Update(fc.Name, fc, metav1.UpdateOptions{})
		if err != nil {
			if !k8serror.IsConflict(err) {
				klog.Error(err)
			}
			return nil, false, err
		}
		var ok bool
		if fc, ok = tmp.(*v1alpha1.ForeignCluster); !ok {
			err = errors.New("retrieved object is not a ForeignCluster")
			klog.Error(err)
			return nil, false, err
		}
		return fc, false, nil
	}
}

func (discovery *DiscoveryCtrl) GetForeignClusterByID(clusterID string) (*v1alpha1.ForeignCluster, error) {
	tmp, err := discovery.crdClient.Resource("foreignclusters").List(metav1.ListOptions{
		LabelSelector: strings.Join([]string{discoveryPkg.ClusterIdLabel, clusterID}, "="),
	})
	if err != nil {
		return nil, err
	}
	fcs, ok := tmp.(*v1alpha1.ForeignClusterList)
	if !ok || len(fcs.Items) == 0 {
		return nil, k8serror.NewNotFound(schema.GroupResource{
			Group:    v1alpha1.GroupVersion.Group,
			Resource: "foreignclusters",
		}, clusterID)
	}
	return &fcs.Items[0], nil
}
