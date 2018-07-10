package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	quobytev1 "github.com/quobyte/k8s-operator/pkg/kubernetes-actors/clientset/versioned"
	quobyte_informers "github.com/quobyte/k8s-operator/pkg/kubernetes-actors/informers/externalversions"
	"github.com/quobyte/k8s-operator/pkg/resourcehandler"
	"github.com/quobyte/k8s-operator/pkg/utils"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	quobytelisters "github.com/quobyte/k8s-operator/pkg/kubernetes-actors/listers/quobyte.com/v1"

	glog "github.com/golang/glog"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	// "k8s.io/client-go/tools/record"
	"github.com/quobyte/k8s-operator/pkg/api/quobyte.com/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/util/workqueue"
)

const (
	controllerName = "quobyte-resource-controller"
	QuobyteClient  = "quobyteClient"
)

type controller struct {
	crdClient              *quobytev1.Clientset
	kubeClient             *kubernetes.Clientset
	clientLister           quobytelisters.QuobyteClientLister
	servicesLister         quobytelisters.QuobyteServiceLister
	daemonsetLister        appslisters.DaemonSetLister
	quobyteClientCRSynced  cache.InformerSynced
	daemonsetSynced        cache.InformerSynced
	quobyteServiceCRSynced cache.InformerSynced

	workqueue workqueue.RateLimitingInterface
}

func (c *controller) Run(stopCh chan struct{}) error {
	defer runtime.HandleCrash()
	defer c.workqueue.ShutDown()
	threads := 2

	glog.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(stopCh, c.quobyteClientCRSynced, c.quobyteServiceCRSynced, c.daemonsetSynced); !ok {
		return fmt.Errorf("failed to resync the caches")
	}
	glog.Info("Finished cache sync")
	glog.Info("Starting workers sync")

	for i := 0; i < threads; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}
	<-stopCh
	glog.Info("Controller stopped")
	return nil
}

func (c *controller) enqueue(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.workqueue.AddRateLimited(key)
}

func (c *controller) runWorker() {
	for c.processNextItem() {
	}
}

func (c *controller) processNextItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.workqueue.Forget(obj)
			runtime.HandleError(fmt.Errorf("expected string type as key but received %#v", obj))
			return nil
		}
		if err := c.syncHandler(key); err != nil {
			return fmt.Errorf("error syncing '%s': %v", key, err)
		}

		c.workqueue.Forget(obj)
		glog.Infof("Sucessfully synced key %s", key)
		return nil
	}(obj)

	if err != nil {
		runtime.HandleError(err)
	}
	return true
}

func (c *controller) syncHandler(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("error processing key %s due to %v", key, err))
		return nil
	}
	quobyteClient, err := c.clientLister.QuobyteClients(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			runtime.HandleError(fmt.Errorf("quobyteclient '%s' no longer exists in queue", key))
			return nil
		}
		return err
	}
	c.handleClientCRUpdate(quobyteClient)
	return nil
}

func newQuobyteController(quobyteclient *quobytev1.Clientset, kubeClient *kubernetes.Clientset, qi quobyte_informers.SharedInformerFactory, ki informers.SharedInformerFactory) (*controller, error) {
	quobyteclientAPI := qi.Quobyte().V1().QuobyteClients()
	quobyteservicesAPI := qi.Quobyte().V1().QuobyteServices()
	daemonsetAPI := ki.Apps().V1().DaemonSets()

	c := &controller{
		clientLister:           quobyteclientAPI.Lister(),
		servicesLister:         quobyteservicesAPI.Lister(),
		daemonsetLister:        daemonsetAPI.Lister(),
		quobyteClientCRSynced:  quobyteclientAPI.Informer().HasSynced,
		quobyteServiceCRSynced: daemonsetAPI.Informer().HasSynced,
		daemonsetSynced:        daemonsetAPI.Informer().HasSynced,
		crdClient:              quobyteclient,
		kubeClient:             kubeClient,
		workqueue:              workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), ""),
	}
	quobyteclientAPI.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.enqueue,
		UpdateFunc: func(old, new interface{}) {
			c.enqueue(new)
		},
		DeleteFunc: func(obj interface{}) {
			glog.Info("Client CR delete event triggered")
			c.handleClientCRDelete(obj)
		},
	})

	quobyteservicesAPI.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			glog.Info("Service CR create event triggered")
			c.handleServicesAdd(obj)
		},
		UpdateFunc: func(old, new interface{}) {
			glog.Info("Service CR update event triggered")
			c.handleServicesCRUpdate(old, new)
		},
		DeleteFunc: func(obj interface{}) {
			glog.Info("Service CR delete event triggered")
			c.handleServicesCRDelete(obj)
		},
	})

	daemonsetAPI.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handleDSChange(obj.(*appsv1.DaemonSet), true)
		},
		UpdateFunc: func(old, new interface{}) {
			newDS := new.(*appsv1.DaemonSet)
			oldDS := old.(*appsv1.DaemonSet)
			if newDS.ResourceVersion == oldDS.ResourceVersion {
				return
			}
			c.handleDSChange(newDS, false)
		},
	})

	return c, nil
}

func (c *controller) handleDSChange(ds *appsv1.DaemonSet, add bool) {
	annotations := ds.Spec.Template.GetAnnotations()
	_, ok := annotations[QuobyteClient]
	if !ok || (!add && !resourcehandler.IsRollingUpdateRequired(ds)) {
		return
	}

	glog.Infof("Processing DaemonSet %s", ds.Name)
	quobyteClientList, err := c.clientLister.QuobyteClients(ds.Namespace).List(labels.Everything())
	var quobyteClient *v1.QuobyteClient
	for _, qbclient := range quobyteClientList {
		if qbclient.Spec.DsName == ds.Name {
			quobyteClient = qbclient
			break
		}
	}
	if quobyteClient == nil {
		return
	}

	if err != nil {
		glog.Errorf("unable to read QuobyteClients due to %v", err)
		return
	}
	c.enqueue(quobyteClient)
}

func (c *controller) handleClientCRAdd(obj interface{}) {
	// syncQuobyteVersion(utils.ClientService, obj.(*v1.QuobyteClient).Spec.Image)
	curCr := obj.(*v1.QuobyteClient)
	dsName := curCr.Spec.DsName
	labelK, labelV, podSelector, ds, err := resourcehandler.ParseDaemonSetForControlFlags(dsName)
	if err != nil {
		glog.Errorf("failed to get labels and selectors for DaemonSet %s due to %v", dsName, err)
		return
	}

	clientNodes := curCr.Spec.Nodes
	if clientNodes != nil {
		resourcehandler.LabelNodes(clientNodes, utils.OperationAdd, labelK, labelV)
		removeNonConfirmingCRNodes(clientNodes, labelK, labelV)
	}
	podImage := resourcehandler.GetImageFromDS(ds)
	if resourcehandler.IsRollingUpdateRequired(ds) {
		if curCr.Spec.RollingUpdate {
			resourcehandler.ControlledPodUpdate(podSelector, podImage, true, curCr, c.crdClient)
		} else {
			printManualUpdateMessage(utils.ClientService)
		}
	} else {
		time.Sleep(time.Duration(1 * time.Minute))
		resourcehandler.ControlledPodUpdate(podSelector, podImage, false, curCr, c.crdClient)
	}
}

// Removes any service that is not defined in the Quobyte config definition
func removeNonConfirmingCRNodes(crNodes []string, labelK, labelV string) {
	nodes, err := utils.GetQuobyteNodes(fmt.Sprintf("%s=%s", labelK, labelV), resourcehandler.KubernetesClient)
	if err != nil {
		glog.Errorf("Unable to get current nodes for the label %s due to %v", labelK, err)
		return
	}

	crNode := make(map[string]string, len(crNodes))
	for _, node := range crNodes {
		crNode[node] = labelV
	}
	for _, node := range nodes.Items {
		_, ok := crNode[node.Name]
		if !ok {
			glog.Infof("Removing node %s from %s services as the node is not part of the Quobyte Configuration. To deploy, update the required quobyte definition (clients/services) with node.", node.Name, strings.Title(labelK))
			resourcehandler.LabelNodes([]string{node.Name}, utils.OperationRemove, labelK, labelV)
		}
	}
}

func (c *controller) handleClientCRDelete(definition interface{}) {
	labelK, labelV, podSelector, _, err := resourcehandler.ParseDaemonSetForControlFlags(definition.(*v1.QuobyteClient).Spec.DsName)
	if err != nil {
		glog.Errorf("failed to get client Daemonset due to %v", err)
		return
	}
	nodes := definition.(*v1.QuobyteClient).Spec.Nodes
	if nodes != nil {
		resourcehandler.LabelNodes(nodes, utils.OperationRemove, labelK, labelV)
		removeNonConfirmingCRNodes(nodes, labelK, labelV)
	}
	resourcehandler.DeletePods(podSelector)
	os.Remove(utils.StatusFile)
	glog.Info("Removed Quobyte client definition (Pod termination signalled)")
}

func (c *controller) handleClientCRUpdate(cur interface{}) {
	curCr := cur.(*v1.QuobyteClient)
	dsName := cur.(*v1.QuobyteClient).Spec.DsName

	glog.Info("QuobyteClient CR updated: Updating Quobyte to the updated template.")
	labelK, labelV, podSelector, ds, err := resourcehandler.ParseDaemonSetForControlFlags(dsName)
	if err != nil {
		glog.Errorf("failed to get labels,selectors for DaemonSet %s due to %v", dsName, err)
		return
	}
	// oldCr := curCr.DeepCopy()
	nodes, err := utils.GetQuobyteNodes(fmt.Sprintf("%s=%s", labelK, labelV), c.kubeClient)
	if err != nil {
		glog.Errorf("%v", err)
		return
	}
	oldNodes := utils.GetNodeNames(nodes)
	curNodes := curCr.Spec.Nodes
	sort.Strings(curNodes)
	if !reflect.DeepEqual(oldNodes, curNodes) {
		handleNodeChanges(oldNodes, curNodes, labelK, labelV)
	}

	if curCr.Spec.Nodes != nil {
		removeNonConfirmingCRNodes(curNodes, labelK, labelV)
	}
	podImage := resourcehandler.GetImageFromDS(ds)
	if resourcehandler.IsRollingUpdateRequired(ds) {
		if curCr.Spec.RollingUpdate {
			resourcehandler.ControlledPodUpdate(podSelector, podImage, true, curCr, c.crdClient)
		} else {
			printManualUpdateMessage(utils.ClientService)
		}
	} else {
		resourcehandler.ControlledPodUpdate(podSelector, podImage, false, curCr, c.crdClient)
	}
}

func nodeDiff(old, cur []string) ([]string, []string) {
	oldN := make(map[string]bool, len(old))
	newN := make(map[string]bool, len(cur))
	var removed, added []string
	for _, o := range old {
		oldN[o] = true
	}
	for _, n := range cur {
		newN[n] = true
	}

	for k, _ := range oldN {
		_, ok := newN[k]
		if ok {
			delete(newN, k)
		} else {
			removed = append(removed, k)
		}
	}

	for nk, _ := range newN {
		added = append(added, nk)
	}
	return removed, added
}

func handleNodeChanges(oldNodes, curNodes []string, labelK, labelV string) {
	oLen := len(oldNodes)
	cLen := len(curNodes)
	if oLen == 0 && cLen == 0 {
		return
	}
	if oLen == 0 && cLen > 0 { // complete new setup
		resourcehandler.LabelNodes(curNodes, utils.OperationAdd, labelK, labelV)
	} else if cLen == 0 && oLen > 0 { //complete removal of existing nodes
		resourcehandler.LabelNodes(oldNodes, utils.OperationRemove, labelK, labelV)
	} else {
		removed, added := nodeDiff(oldNodes, curNodes)
		resourcehandler.LabelNodes(added, utils.OperationAdd, labelK, labelV)
		resourcehandler.LabelNodes(removed, utils.OperationRemove, labelK, labelV)
	}
}

// Start starts quobyte crd monitoring
func Start(quobyteclient *quobytev1.Clientset, kubeClient *kubernetes.Clientset) {
	quobyteInfoFactory := quobyte_informers.NewSharedInformerFactory(quobyteclient, 5*time.Minute)
	kubeInfoFactory := informers.NewSharedInformerFactory(kubeClient, 30*time.Second)
	controller, err := newQuobyteController(quobyteclient, kubeClient, quobyteInfoFactory, kubeInfoFactory)

	if err != nil {
		glog.Errorf("Terminating operator due to:\n %v ", err)
	}
	stopCh := make(chan struct{})
	defer close(stopCh)

	go quobyteInfoFactory.Start(stopCh)
	go kubeInfoFactory.Start(stopCh)

	go controller.Run(stopCh)

	termSig := make(chan os.Signal, 1)
	signal.Notify(termSig, syscall.SIGTERM)
	signal.Notify(termSig, syscall.SIGINT)
	<-termSig
}

func (c *controller) handleServicesAdd(obj interface{}) {
	registry := obj.(*v1.QuobyteService).Spec.RegistryService
	data := obj.(*v1.QuobyteService).Spec.DataService
	metadata := obj.(*v1.QuobyteService).Spec.MetadataService
	regNodes := registry.Nodes
	if regNodes != nil {
		labelK, labelV, _, _, err := resourcehandler.ParseDaemonSetForControlFlags(registry.DsName)
		if err == nil {
			resourcehandler.LabelNodes(regNodes, utils.OperationAdd, labelK, labelV)
			removeNonConfirmingCRNodes(regNodes, labelK, labelV)
		}
	}
	dataNodes := data.Nodes
	if dataNodes != nil {
		labelK, labelV, _, _, err := resourcehandler.ParseDaemonSetForControlFlags(data.DsName)
		if err == nil {
			resourcehandler.LabelNodes(dataNodes, utils.OperationAdd, labelK, labelV)
			removeNonConfirmingCRNodes(dataNodes, labelK, labelV)
		}
	}
	metadataNodes := metadata.Nodes
	if metadataNodes != nil {
		labelK, labelV, _, _, err := resourcehandler.ParseDaemonSetForControlFlags(metadata.DsName)
		if err == nil {
			resourcehandler.LabelNodes(metadataNodes, utils.OperationAdd, labelK, labelV)
			removeNonConfirmingCRNodes(metadataNodes, labelK, labelV)
		}
	}
}

func appendStatus(statusVal []byte, status interface{}) {
	if statusVal == nil {
		return
	}
	err := json.Unmarshal(statusVal, status)
	if err != nil {
		glog.Errorf("Unable to convert status to JSON due %v", err)
	}
}

func (c *controller) handleServicesCRUpdate(old, cur interface{}) {
	oldCr := old.(*v1.QuobyteService)
	curCr := cur.(*v1.QuobyteService)

	oldRegistry := oldCr.Spec.RegistryService
	newRegistry := curCr.Spec.RegistryService
	oldData := oldCr.Spec.DataService
	newData := curCr.Spec.DataService
	oldMetadata := oldCr.Spec.MetadataService
	newMetadata := curCr.Spec.MetadataService

	regK, regV, _, _, _ := resourcehandler.ParseDaemonSetForControlFlags(newRegistry.DsName)
	dataK, dataV, _, _, _ := resourcehandler.ParseDaemonSetForControlFlags(newData.DsName)
	metaK, metaV, _, _, _ := resourcehandler.ParseDaemonSetForControlFlags(newMetadata.DsName)

	if !reflect.DeepEqual(oldCr, curCr) {

		if !reflect.DeepEqual(oldRegistry.Nodes, newRegistry.Nodes) {
			glog.Infof("Registry nodes changed: old %v and new %v", oldRegistry.Nodes, newRegistry.Nodes)
			handleNodeChanges(oldRegistry.Nodes, newRegistry.Nodes, regK, regV)
		}

		if !reflect.DeepEqual(oldData.Nodes, newData.Nodes) {
			glog.Infof("Data service nodes changed: old %v and new %v", oldData.Nodes, newData.Nodes)
			handleNodeChanges(oldData.Nodes, newData.Nodes, dataK, dataV)
		}

		if !reflect.DeepEqual(oldMetadata.Nodes, newMetadata.Nodes) {
			glog.Infof("Metadata service nodes changed: old %v and new %v", oldMetadata.Nodes, newMetadata.Nodes)
			handleNodeChanges(oldMetadata.Nodes, newMetadata.Nodes, metaK, metaV)
		}
	}
	if newRegistry.Nodes != nil {
		removeNonConfirmingCRNodes(newRegistry.Nodes, regK, regV)
	}
	if newData.Nodes != nil {
		removeNonConfirmingCRNodes(newData.Nodes, dataK, dataV)
	}
	if newMetadata.Nodes != nil {
		removeNonConfirmingCRNodes(newMetadata.Nodes, metaK, metaV)
	}
}

func printManualUpdateMessage(svc string) {
	glog.Infof("****Rolling update disabled for %s service. Requires manual deletion of pods to update the Quobyte version.****", svc)
}

func (c *controller) handleServicesCRDelete(definition interface{}) {
	servicesSpec := definition.(*v1.QuobyteService).Spec
	regNodes := servicesSpec.RegistryService.Nodes
	dataNodes := servicesSpec.DataService.Nodes
	metaNodes := servicesSpec.MetadataService.Nodes
	if regNodes != nil {
		labelK, labelV, selector, _, err := resourcehandler.ParseDaemonSetForControlFlags(servicesSpec.RegistryService.DsName)
		if err == nil {
			resourcehandler.LabelNodes(regNodes, utils.OperationRemove, labelK, labelV)
			removeNonConfirmingCRNodes(regNodes, labelK, labelV)
			resourcehandler.DeletePods(selector)
		}
	}
	if dataNodes != nil {
		labelK, labelV, selector, _, err := resourcehandler.ParseDaemonSetForControlFlags(servicesSpec.DataService.DsName)
		if err == nil {
			resourcehandler.LabelNodes(dataNodes, utils.OperationRemove, labelK, labelV)
			removeNonConfirmingCRNodes(dataNodes, labelK, labelV)
			resourcehandler.DeletePods(selector)
		}
	}
	if metaNodes != nil {
		labelK, labelV, selector, _, err := resourcehandler.ParseDaemonSetForControlFlags(servicesSpec.MetadataService.DsName)
		if err == nil {
			resourcehandler.LabelNodes(metaNodes, utils.OperationRemove, labelK, labelV)
			removeNonConfirmingCRNodes(metaNodes, labelK, labelV)
			resourcehandler.DeletePods(selector)
		}
	}
	glog.Info("Removed Quobyte service definition (Pod termination signalled)")
}
