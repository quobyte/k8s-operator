package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type QuobyteClient struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            string `json:"status"`

	Spec QuobyteClientSpec `json:",spec"`
}

// type QuobyteClientStatus struct {
// 	Items []ClientUpdateOnHold `json:"items"`
// }

// ClientUpdateOnHold Client pod that is not updated by operator.
// Operator may not update some pods due to early exist of updating on first update failure or
// node has some application pods that are accessing the QuobyteVolume either through PVC/PV or Volume reference.
// type ClientUpdateOnHold struct {
// 	Node          string // node on which client update is held by pods
// 	Pod           string
// 	ExpectedImage string
// 	CurrentImage  string
// 	BlockingPods  []string // pods using QuobyteVolume
// }

// QuobyteClientSpec contains spec for quobyte client resource.
type QuobyteClientSpec struct {
	Service
	Trigger       string `json:"trigger"`
	RollingUpdate bool   `json:"rollingUpdatesEnabled"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// QuobyteClientList is list of quobyteclients
type QuobyteClientList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []QuobyteClient `json:"items"`
}
