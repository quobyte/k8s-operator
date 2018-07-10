package resourcehandler

import (
	"encoding/json"
	"fmt"
	"io/ioutil"

	"github.com/quobyte/k8s-operator/pkg/utils"
	// "github.com/quobyte/k8s-operator/pkg/utils"

	"strings"
	"time"

	quobyteClient "github.com/quobyte/k8s-operator/pkg/kubernetes-actors/clientset/versioned"

	"github.com/golang/glog"
	quobytev1 "github.com/quobyte/k8s-operator/pkg/api/quobyte.com/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	podCreateMaxRetries int = 5
)

type ClientUpdateOnHold struct {
	Node          string // node on which client update is held by pods
	Pod           string
	ExpectedImage string
	CurrentImage  string
	BlockingPods  []string `json:",omitempty"` // pods using QuobyteVolume
}

// GetPodsWithSelector gives pods with specified selector. The selector must be valid label selector supported by API.
func GetPodsWithSelector(selector string) (podlist *v1.PodList) {
	podlist, err := KubernetesClient.CoreV1().Pods(quobyteNameSpace).List(metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		glog.Errorf("Failed to get pods with selector %s due to %s", selector, err)
		return nil
	}
	return
}

// DeletePods deletes pods by given selector.
func DeletePods(selector string) {
	list := GetPodsWithSelector(selector)
	if list != nil {
		for _, pod := range list.Items {
			err := KubernetesClient.CoreV1().Pods(quobyteNameSpace).Delete(pod.Name, &metav1.DeleteOptions{})
			if err != nil {
				glog.Errorf("Failed to delete pod %s due to %s", pod.Name, err)
			}
		}
	}
}

// ControlledPodUpdate Deletes given list of pods. This triggers redeployment of updated pods.
// Currently, this method checks pod recreation on node 5 times with each try dealyed by 60 secs.
// If Pod not running within 5 tries past the Pending pod status, this reports Failure and exits update process.
func ControlledPodUpdate(selector, image string, update bool, quobyteClient *quobytev1.QuobyteClient, crdClient *quobyteClient.Clientset) ([]byte, error) {
	role := "client"
	podList := GetPodsWithSelector(selector)
	status := make([]*ClientUpdateOnHold, 0, len(podList.Items))
	podFailed := false // first update pod sets the message, stopping all other pods of the same service from updating.
	for _, pod := range podList.Items {
		if pod.Spec.Containers[0].Image == image {
			glog.Infof("%s pod already has the requsted image %s", pod.Name, image)
			status = append(status, &ClientUpdateOnHold{pod.Spec.NodeName, pod.Name, image, pod.Spec.Containers[0].Image, nil})
			continue
		} else {
			glog.Infof("%s pod running with different version", pod.Name)
		}
		if update {
			// Don't let any pod schedule on this node temporarily.
			AddUpgradeTaint(pod.Spec.NodeName)
		}
		blockingPodsInfo := checkQuobyteVolumeMounts(pod, image, update)
		if blockingPodsInfo != nil {
			status = append(status, blockingPodsInfo)
			continue
		}
		// add status of the current pod for status requests and skip upgrade
		if !update {
			status = append(status, &ClientUpdateOnHold{pod.Spec.NodeName, pod.Name, image, pod.Spec.Containers[0].Image, nil})
			continue
		} else {
			if !podFailed { // No previous failure, so continue update.
				var updatedPod v1.Pod
				err = KubernetesClient.CoreV1().Pods(quobyteNameSpace).Delete(pod.Name, &metav1.DeleteOptions{})
				if err != nil {
					RemoveUpgradeTaint(pod.Spec.NodeName)
					glog.Errorf("Exiting %s update. Failed to delete pod %s due to %v", role, pod.Name, err)
				}
				podRunning := false
				retryCount := 1
				for !podRunning && retryCount <= podCreateMaxRetries {
					podList, err := KubernetesClient.CoreV1().Pods(quobyteNameSpace).List(metav1.ListOptions{LabelSelector: fmt.Sprintf("role=%s", role), FieldSelector: fmt.Sprintf("spec.nodeName=%s", pod.Spec.NodeName)})
					if err != nil {
						glog.Errorf("Failed to list updated %s pod on node %s due to %v", role, pod.Spec.NodeName, err)
						podFailed = true
						RemoveUpgradeTaint(pod.Spec.NodeName)
						continue
					}
					if len(podList.Items) > 0 {
						updatedPod = podList.Items[0]
						podRunning, err = isPodRunning(updatedPod, pod.Name)
						if !podRunning {
							if err == nil { // Count retry only if pod is past Pending status
								retryCount++
							}
							glog.Infof("Updated Pod is not running on node %s\nretry: %d of configured max retries: %d", pod.Spec.NodeName, retryCount, podCreateMaxRetries)
							time.Sleep(time.Duration(retryCount*30) * time.Second)
						} else {
							glog.Infof("%s pod is running. Continuing next pod update", updatedPod.Name)
							time.Sleep(30 * time.Second)
						}
					}
				}
				if !podRunning {
					RemoveUpgradeTaint(pod.Spec.NodeName)
					glog.Errorf("Exiting update: Failed to create updated %s pod within %d retries", role, retryCount)
					podFailed = false
				}
				status = append(status, &ClientUpdateOnHold{pod.Spec.NodeName, updatedPod.Name, image, updatedPod.Spec.Containers[0].Image, nil})
				RemoveUpgradeTaint(pod.Spec.NodeName)
			} else {
				status = append(status, &ClientUpdateOnHold{pod.Spec.NodeName, pod.Name, image, pod.Spec.Containers[0].Image, nil})
			}
		}
	}

	out, _ := json.Marshal(status)

	if out != nil {
		err := ioutil.WriteFile(utils.StatusFile, out, 0777)
		if err != nil {
			glog.Error(err.Error())
		} else {
			glog.Info("Finished writing status object")
		}
	}

	// update status object of the current QuobyteClient CR
	qc := quobyteClient.DeepCopy()
	qc.Status = fmt.Sprintf("updated completed at %v", time.Now())
	_, err := crdClient.QuobyteV1().QuobyteClients(qc.Namespace).UpdateStatus(qc)
	if err != nil {
		glog.Errorf("unable to update the status due to %v", err)
	}
	return nil, nil
}

func checkQuobyteVolumeMounts(pod v1.Pod, image string, update bool) *ClientUpdateOnHold {
	lblSelector := "role  notin (registry,client,data,metadata,data,qmgmt-pod,webconsole)"
	pods, err := KubernetesClient.CoreV1().Pods("").List(metav1.ListOptions{LabelSelector: lblSelector, FieldSelector: fmt.Sprintf("spec.nodeName=%s", pod.Spec.NodeName)})
	if err != nil {
		if update {
			RemoveUpgradeTaint(pod.Spec.NodeName)
		}
		message := fmt.Sprintf("skipping client update. Cannot check the pods accessing Quobyte volume. Failed to get the pods on %s due to %v", pod.Spec.NodeName, err)
		glog.Error(message)
		return &ClientUpdateOnHold{pod.Spec.NodeName, pod.Name, image, pod.Spec.Containers[0].Image, nil}
	}
	QuobyteVolumePods := make([]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		for _, volume := range pod.Spec.Volumes {
			if volume.VolumeSource.Quobyte != nil {
				QuobyteVolumePods = append(QuobyteVolumePods, pod.Name)
			} else if volume.VolumeSource.PersistentVolumeClaim != nil {
				claim := volume.VolumeSource.PersistentVolumeClaim.ClaimName
				pvc, err := KubernetesClient.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(claim, metav1.GetOptions{})
				if err != nil {
					glog.Errorf("unable to get the PVC for pod %s for the claim %s due to %v", pod.Name, claim, err)
					continue
				}
				storageClass, err := KubernetesClient.StorageV1().StorageClasses().Get(*pvc.Spec.StorageClassName, metav1.GetOptions{})
				if err != nil {
					glog.Errorf("unable to get the storage class %s for the PVC %s", storageClass, pvc.Name)
					continue
				}
				if strings.Contains(storageClass.Provisioner, "quobyte") {
					QuobyteVolumePods = append(QuobyteVolumePods, pod.Name)
				}
			}
		}
	}
	if len(QuobyteVolumePods) > 0 {
		return &ClientUpdateOnHold{pod.Spec.NodeName, pod.Name, image, pod.Spec.Containers[0].Image, QuobyteVolumePods}
	}
	return nil
}

func isPodRunning(pod v1.Pod, deletePodName string) (bool, error) {
	if pod.Name == deletePodName {
		return false, fmt.Errorf("Pending")
	}
	switch pod.Status.Phase {
	case v1.PodFailed, v1.PodSucceeded:
		return false, nil
	case v1.PodRunning:
		for _, cond := range pod.Status.Conditions {
			if cond.Type != v1.PodReady {
				continue
			}
			return cond.Status == v1.ConditionTrue, nil
		}
		return false, nil
	case v1.PodPending:
		return false, fmt.Errorf("Pending")
	}
	return false, nil
}
