package main

import (
	"flag"
	"os"

	"github.com/quobyte/k8s-operator/pkg/controller"
	"github.com/quobyte/k8s-operator/pkg/resourcehandler"
	"github.com/quobyte/k8s-operator/pkg/utils"
	"github.com/quobyte/k8s-operator/pkg/web"

	"github.com/golang/glog"
)

func main() {
	kubeconfig := ""
	flag.StringVar(&kubeconfig, "kubeconfig", kubeconfig, "kubeconfig file not found")
	flag.Parse()
	flag.Lookup("logtostderr").Value.Set("true")
	flag.Lookup("v").Value.Set("1")
	glog.Info("Starting Quobyte operator")
	resourcehandler.InitClient(kubeconfig)
	if resourcehandler.KubernetesClient != nil {
		glog.Info("Starting operator UI")
		go web.StartWebServer()
	}

	err := resourcehandler.CreateAllQuobyteCrd()
	if err != nil {
		glog.Errorf("Terminating operator due to: \n %v", err)
		os.Exit(1)
	}

	os.Remove(utils.StatusFile)
	glog.Info("Starting operator")
	controller.Start(resourcehandler.CrdClient, resourcehandler.KubernetesClient)
}
