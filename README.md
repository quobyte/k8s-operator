# k8s-operator

## Build
1. Clone the repository.
```
git clone git@github.com:quobyte/k8s-operator.git github.com/quobyte/k8s-operator
```
2. Compile and build binary from source.
```
cd github.com/quobyte/k8s-operator
export GOPATH=$(pwd)
./build #build the operator binary
```
If you're building for the first time after clone run ``glide install --strip-vendor`` to get the dependencies.

3. To run operator outside cluster (skip to 4 to run operator inside cluster)
```
./operator --kubeconfig <kuberenetes-admin-conf>
```
  Follow [Deploy clients](#deploy-clients), and you can skip step 3 of deploy clients.

4. Build the container and push it to repository
``
./build <repository-url> # push the built image to the container repository-url
``
