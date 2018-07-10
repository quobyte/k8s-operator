package resourcehandler

import (
	"os"

	quobytev1 "github.com/quobyte/k8s-operator/pkg/kubernetes-actors/clientset/versioned"

	"github.com/golang/glog"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	config           *rest.Config
	CrdClient        *quobytev1.Clientset
	err              error
	KubernetesClient *kubernetes.Clientset
	APIServerClient  *apiextensionsclient.Clientset
	quobyteNameSpace = "quobyte"
)

//InitClient Initializes kubernetes client for given kubeconfig
func InitClient(kubeconfig string) {
	if kubeconfig == "" { // must be running inside cluster, try to get from env
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		glog.Errorf("Error creating kubernetes access client: %s", err)
		os.Exit(1)
	}
	KubernetesClient, err = kubernetes.NewForConfig(config)

	if err != nil {
		panic("Unable to create kubernetes client for the given configuration")
	}
	CrdClient, err = quobytev1.NewForConfig(config)

	if err != nil {
		panic("Unable to create api extentions client for the given configuration")
	}

	cfg2 := *config
	cfg2.GroupVersion = &schema.GroupVersion{
		Group:   "quobyte.com",
		Version: "v1",
	}
	cfg2.APIPath = "/apis"

	APIServerClient, err = apiextensionsclient.NewForConfig(&cfg2)

	glog.Info("Initialized kubernetes cluster access clients")
}
