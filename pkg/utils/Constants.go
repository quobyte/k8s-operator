package utils

const (
	// OperationRemove instructs label remove operation.
	OperationRemove = "remove"
	// OperationAdd instructs label add operation.
	OperationAdd = "add"
	// RegistryService name of the registry service, expectation is daemonset of the service has the same name.
	RegistryService = "registry"
	// MetadataService name of the registry service, expectation is daemonset of the service has the same name.
	MetadataService = "metadata"
	// DataService name of the registry service, expectation is daemonset of the service has the same name.
	DataService = "data"
	// ClientService name of the registry service, expectation is daemonset of the service has the same name.
	ClientService    = "client"
	RegistrySelector = "role=registry"
	DataSelector     = "role=data"
	MetadataSelector = "role=metadata"
	StatusFile       = "/public/status"
)
