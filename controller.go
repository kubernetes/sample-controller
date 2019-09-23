/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"io/ioutil"
	"os/exec"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	samplev1alpha1 "k8s.io/sample-controller/pkg/apis/samplecontroller/v1alpha1"
	clientset "k8s.io/sample-controller/pkg/generated/clientset/versioned"
	samplescheme "k8s.io/sample-controller/pkg/generated/clientset/versioned/scheme"
	informers "k8s.io/sample-controller/pkg/generated/informers/externalversions/samplecontroller/v1alpha1"
	listers "k8s.io/sample-controller/pkg/generated/listers/samplecontroller/v1alpha1"
)

const controllerAgentName = "sample-controller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a SessionJob is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a SessionJob fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by SessionJob"
	// MessageResourceSynced is the message used for an Event fired when a SessionJob
	// is synced successfully
	MessageResourceSynced = "SessionJob synced successfully"
)

// Controller is the controller implementation for SessionJob resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// sampleclientset is a clientset for our own API group
	sampleclientset clientset.Interface

	deploymentsLister appslisters.DeploymentLister
	deploymentsSynced cache.InformerSynced
	sessionjobsLister listers.SessionJobLister
	sessionjobsSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new sample controller
func NewController(
	kubeclientset kubernetes.Interface,
	sampleclientset clientset.Interface,
	deploymentInformer appsinformers.DeploymentInformer,
	sessionjobInformer informers.SessionJobInformer) *Controller {

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilruntime.Must(samplescheme.AddToScheme(scheme.Scheme))
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:     kubeclientset,
		sampleclientset:   sampleclientset,
		deploymentsLister: deploymentInformer.Lister(),
		deploymentsSynced: deploymentInformer.Informer().HasSynced,
		sessionjobsLister: sessionjobInformer.Lister(),
		sessionjobsSynced: sessionjobInformer.Informer().HasSynced,
		workqueue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "SessionJobs"),
		recorder:          recorder,
	}

	klog.Info("Setting up event handlers")
	// Set up an event handler for when SessionJob resources change
	sessionjobInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueSessionJob,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueSessionJob(new)
		},
	})
	// Set up an event handler for when Deployment resources change. This
	// handler will lookup the owner of the given Deployment, and if it is
	// owned by a SessionJob resource will enqueue that SessionJob resource for
	// processing. This way, we don't need to implement custom logic for
	// handling Deployment resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	deploymentInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newDepl := new.(*appsv1.Deployment)
			oldDepl := old.(*appsv1.Deployment)
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting SessionJob controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.deploymentsSynced, c.sessionjobsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process SessionJob resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// SessionJob resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the SessionJob resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the SessionJob resource with this namespace/name
	sessionjob, err := c.sessionjobsLister.SessionJobs(namespace).Get(name)
	if err != nil {
		// The SessionJob resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("sessionjob '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	deploymentName := sessionjob.Spec.DeploymentName
	if deploymentName == "" {
		// We choose to absorb the error here as the worker would requeue the
		// resource otherwise. Instead, the next time the resource is updated
		// the resource will be queued again.
		utilruntime.HandleError(fmt.Errorf("%s: deployment name must be specified", key))
		return nil
	}

	// Get the deployment with the name specified in SessionJob.spec
	deployment, err := c.deploymentsLister.Deployments(sessionjob.Namespace).Get(deploymentName)
	// If the resource doesn't exist, we'll create it
	if errors.IsNotFound(err) {
		deployment, err = c.kubeclientset.AppsV1().Deployments(sessionjob.Namespace).Create(newDeployment(sessionjob))

		//use the deployment as a flag, only run the client when the deployment is created.
		//parameter := fmt.Sprintf("-m %d -r %d", *sessionjob.Spec.TaskCount, *sessionjob.Spec.TaskRuntime)
		command := "/opt/ibm/sym-client-controller/sym.sh " + sessionjob.Spec.TaskInput + " " + sessionjob.Spec.TaskFunction + " " + sessionjob.Spec.TaskOutput
		go func() {
			cmd := exec.Command("/bin/bash", "-c", command)
			_, err := cmd.Output()
			if err != nil {
				fmt.Println(err)
			}
			cmd.Wait()
		}()
	}

	// If an error occurs during Get/Create, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// If the Deployment is not controlled by this SessionJob resource, we should log
	// a warning to the event recorder and ret
	if !metav1.IsControlledBy(deployment, sessionjob) {
		msg := fmt.Sprintf(MessageResourceExists, deployment.Name)
		c.recorder.Event(sessionjob, corev1.EventTypeWarning, ErrResourceExists, msg)
		return fmt.Errorf(msg)
	}

	// If this number of the replicas on the SessionJob resource is specified, and the
	// number does not equal the current desired replicas on the Deployment, we
	// should update the Deployment resource.
	if sessionjob.Spec.Replicas != nil && *sessionjob.Spec.Replicas != *deployment.Spec.Replicas {
		klog.V(4).Infof("SessionJob %s replicas: %d, deployment replicas: %d", name, *sessionjob.Spec.Replicas, *deployment.Spec.Replicas)
		deployment, err = c.kubeclientset.AppsV1().Deployments(sessionjob.Namespace).Update(newDeployment(sessionjob))
	}

	// If an error occurs during Update, we'll requeue the item so we can
	// attempt processing again later. THis could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// Finally, we update the status block of the SessionJob resource to reflect the
	// current state of the world
	err = c.updateSessionJobStatus(sessionjob, deployment)
	if err != nil {
		return err
	}

	c.recorder.Event(sessionjob, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

func (c *Controller) updateSessionJobStatus(sessionjob *samplev1alpha1.SessionJob, deployment *appsv1.Deployment) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance
	sessionjobCopy := sessionjob.DeepCopy()
	//sessionjobCopy.Status.AvailableReplicas = deployment.Status.AvailableReplicas

	command := "/opt/ibm/sym-client-controller/sym_monitor.sh"
	cmd := exec.Command("/bin/bash", "-c", command)
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()
	content, err := ioutil.ReadAll(stdout)
	if err != nil {
		fmt.Println(err)
	} else {
		results := strings.Split(string(content), " ")

		/* the output is as following, and splitted by xargs, parse them here
		......
		Running tasks:                               0
		Pending tasks in open sessions:              0
		......
		Done tasks since SSM started:                3
		......
		*/

		sessionjobCopy.Status.RunningTasks = 0
		sessionjobCopy.Status.PendingTasks = 0
		sessionjobCopy.Status.DoneTasks = 0

		for i := 0; i < len(results); i++ {
			if strings.EqualFold(results[i], "Running") && strings.EqualFold(results[i+1], "tasks:") {
				ret, err := strconv.ParseInt(results[i+2], 10, 32)
				if err != nil {
					fmt.Println(err)
				} else {
					sessionjobCopy.Status.RunningTasks = int32(ret)
				}
			}

			if strings.EqualFold(results[i], "Pending") && strings.EqualFold(results[i+1], "tasks") && strings.EqualFold(results[i+2], "in") && strings.EqualFold(results[i+3], "open") && strings.EqualFold(results[i+4], "sessions:") {
				ret, err := strconv.ParseInt(results[i+5], 10, 32)
				if err != nil {
					fmt.Println(err)
				} else {
					sessionjobCopy.Status.PendingTasks = int32(ret)
				}
			}

			if strings.EqualFold(results[i], "Done") && strings.EqualFold(results[i+1], "tasks") && strings.EqualFold(results[i+2], "since") && strings.EqualFold(results[i+3], "SSM") && strings.EqualFold(results[i+4], "started:") {
				ret, err := strconv.ParseInt(results[i+5], 10, 32)
				if err != nil {
					fmt.Println(err)
				} else {
					sessionjobCopy.Status.DoneTasks = int32(ret)
				}
			}
		}
	}
	cmd.Wait()

	// If the CustomResourceSubresources feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the SessionJob resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	//_, err := c.sampleclientset.SamplecontrollerV1alpha1().SessionJobs(sessionjob.Namespace).Update(sessionjobCopy)
	_, err = c.sampleclientset.SamplecontrollerV1alpha1().SessionJobs(sessionjob.Namespace).UpdateStatus(sessionjobCopy)
	return err
}

// enqueueSessionJob takes a SessionJob resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than SessionJob.
func (c *Controller) enqueueSessionJob(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the SessionJob resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that SessionJob resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.V(4).Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a SessionJob, we should not do anything more
		// with it.
		if ownerRef.Kind != "SessionJob" {
			return
		}

		sessionjob, err := c.sessionjobsLister.SessionJobs(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			klog.V(4).Infof("ignoring orphaned object '%s' of sessionjob '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}

		c.enqueueSessionJob(sessionjob)
		return
	}
}

// newDeployment creates a new Deployment for a SessionJob resource. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the SessionJob resource that 'owns' it.
func newDeployment(sessionjob *samplev1alpha1.SessionJob) *appsv1.Deployment {
	labels := map[string]string{
		"app":        "nginx",
		"controller": sessionjob.Name,
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sessionjob.Spec.DeploymentName,
			Namespace: sessionjob.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(sessionjob, samplev1alpha1.SchemeGroupVersion.WithKind("SessionJob")),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: sessionjob.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
}
