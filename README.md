# sample-controller

This repository implements a simple controller for watching SessionJob resources as
defined with a CustomResourceDefinition (CRD).

This particular example demonstrates how to perform basic Symphony client operations such as:

* How to specify the task input/output path and the callback fucntion.
* How to get the task status.

## Details


### Fetch with godep

When NOT using go 1.11 modules, you can use the following commands.

```sh
go get -d k8s.io/sample-controller
cd $GOPATH/src/k8s.io/sample-controller
godep restore
```

### When using go 1.11 modules

When using go 1.11 modules (`GO111MODULE=on`), issue the following
commands --- starting in whatever working directory you like.

```sh
git clone https://github.com/kubernetes/sample-controller.git
cd sample-controller
```

## Running

```sh
# deploy the FaaS sample in the Symhony client container, the FaaS package (https://www.ibm.com/support/knowledgecenter/SSZUMP_7.3.0/prototypes/faas_installing_linux.html) is only for 7.3 now, you need to update the version in the application profile, copy related files (input.txt myfunc.py) to folder /share.

# assumes you have a working kubeconfig, not required if operating in-cluster
go build -o sample-controller .

# copy sample-controller sym.sh sym_monitor.sh into the /opt/ibm/sample-controller folder of the Symhony client container, grant 775 permission for the .sh files and then run
./sample-controller

# create a CustomResourceDefinition
kubectl create -f artifacts/examples/crd-status-subresource.yaml

# create a custom resource of type SessionJob to run tasks
kubectl create -f artifacts/examples/example-sessionjob.yaml

# check task status through the custom resource
kubectl describe SessionJob

Part of the result is like:

Spec:
  Deployment Name:  example-sessionjob
  Replicas:         1
  Task Function:    /share/myfunc.py
  Task Input:       /share/input.txt
  Task Output:      /share/output.txt
Status:
  Done Tasks:     100
  Pending Tasks:  72
  Running Tasks:  8

```

## Cleanup

You can clean up the created CustomResourceDefinition with:

    kubectl delete crd sessionjobs.samplecontroller.k8s.io
