package resourcehandler

import (
	"fmt"

	"github.com/golang/glog"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetDaemonsetByName(name string) (*appsv1.DaemonSet, error) {
	return KubernetesClient.Apps().DaemonSets(quobyteNameSpace).Get(name, metav1.GetOptions{})
}

func IsRollingUpdateRequired(ds *appsv1.DaemonSet) bool {
	return ds.Status.DesiredNumberScheduled != ds.Status.UpdatedNumberScheduled
}

func ParseDaemonSetForControlFlags(dsName string) (string, string, string, *appsv1.DaemonSet, error) {
	if dsName == "" {
		return "", "", "", nil, fmt.Errorf("Daemonset name is empty")
	}
	ds, err := GetDaemonsetByName(dsName)
	if err != nil {
		glog.Errorf("failed to get DaemonSet %s due to %v", dsName, err)
		return "", "", "", nil, err
	}
	selectors := ds.Spec.Template.Spec.NodeSelector
	var k, v string
	for k, v = range selectors {
		break
	}
	var lk, lv string
	for lk, lv = range ds.Spec.Selector.MatchLabels {
		break
	}
	return k, v, fmt.Sprintf("%s=%s", lk, lv), ds, err
}

func GetImageFromDS(ds *appsv1.DaemonSet) string {
	return ds.Spec.Template.Spec.Containers[0].Image
}
