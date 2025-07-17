package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubectl/pkg/scheme"
)

// Controller is a controller that watches and reconciles the node
type Controller struct {
	nodeName string

	logger *slog.Logger

	client          kubernetes.Interface
	informerFactory informers.SharedInformerFactory
	recorder        record.EventRecorder
	nodesLister     corelisters.NodeLister
	nodesSynced     cache.InformerSynced
	queue           workqueue.TypedRateLimitingInterface[cache.ObjectName]
}

func NewController(ctx context.Context, nodemetadata NodeMetadata) (*Controller, error) {
	// Build the Kubernetes client configuration
	config, err := clientcmd.BuildConfigFromFlags(nodemetadata.ClusterURL, "")
	if err != nil {
		return nil, fmt.Errorf("failed to build Kubernetes client configuration: %w", err)
	}

	// Set the IAM bearer token for authentication
	config.BearerToken = nodemetadata.Token

	// Set the CA certificate from the node metadata
	decodedCA, err := base64.StdEncoding.DecodeString(nodemetadata.ClusterCA)
	if err != nil {
		return nil, fmt.Errorf("failed to decode Kubernetes CA certificate: %w", err)
	}
	config.CAData = decodedCA

	// Create the Kubernetes client
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create the node informer with a field selector to watch only the current node
	fieldSelector := fmt.Sprintf("metadata.name=%s", nodemetadata.Name)
	tweakListOptions := func(options *metav1.ListOptions) {
		options.FieldSelector = fieldSelector
	}
	informerFactory := informers.NewSharedInformerFactoryWithOptions(client, time.Hour*24, informers.WithTweakListOptions(tweakListOptions))
	nodeInformer := informerFactory.Core().V1().Nodes()

	// Define the rate limiter for the workqueue
	ratelimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[cache.ObjectName](5*time.Millisecond, 1000*time.Second),
		&workqueue.TypedBucketRateLimiter[cache.ObjectName]{Limiter: rate.NewLimiter(rate.Limit(50), 300)},
	)

	// Create the recorder for events
	eventBroadcaster := record.NewBroadcaster(record.WithContext(ctx))
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: client.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "agent"})

	// Create the controller
	controller := &Controller{
		nodeName:        nodemetadata.Name,
		client:          client,
		informerFactory: informerFactory,
		recorder:        recorder,
		nodesLister:     nodeInformer.Lister(),
		nodesSynced:     nodeInformer.Informer().HasSynced,
		queue:           workqueue.NewTypedRateLimitingQueue(ratelimiter),
		logger:          slog.Default(),
	}

	// Set up the informer to watch for changes to the node
	_, err = nodeInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			newNode := obj.(*corev1.Node)
			controller.queue.Add(cache.ObjectName{Namespace: newNode.Namespace, Name: newNode.Name})
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			updatedNode := newObj.(*corev1.Node)
			controller.queue.Add(cache.ObjectName{Namespace: updatedNode.Namespace, Name: updatedNode.Name})
		},
		DeleteFunc: func(obj interface{}) {
			deletedNode := obj.(*corev1.Node)
			controller.queue.Add(cache.ObjectName{Namespace: deletedNode.Namespace, Name: deletedNode.Name})
		},
	}, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to set up event handler for node informer: %w", err)
	}

	return controller, nil
}

func (c *Controller) Run(ctx context.Context) error {
	// Start the informer factories to begin populating the informer caches
	c.logger.Info("Starting controller")
	go c.informerFactory.Start(ctx.Done())

	// Wait for the cache to be synced before starting worker
	c.logger.Info("Waiting for informer cache to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.nodesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// Start the worker
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			c.logger.Info("Defer worker stopped")
		}()

		wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}()
	c.logger.Info("Starting worker")

	// Block until the context is done and gracefully shut down the worker
	<-ctx.Done()
	c.logger.Info("Shutting down worker")
	c.queue.ShutDown()

	// Wait for the worker to finish
	wg.Wait()
	c.logger.Info("Worker stopped")

	return nil
}

// runWorker starts a single worker.
func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.queue.Get()

	if shutdown {
		return false
	}

	defer c.queue.Done(objRef)

	err := c.syncHandler(ctx)
	if err == nil {
		c.queue.Forget(objRef)
		return true
	}

	c.logger.Error("Sync error, requeuing", slog.Any("error", err))
	c.queue.AddRateLimited(objRef)
	return true
}

// syncNode runs the node reconciliation logic.
func (c *Controller) syncHandler(ctx context.Context) error {

	// Upgrade the node if the annotation is set
	err := c.upgradeNode(ctx)
	if err != nil {
		return fmt.Errorf("failed to upgrade node %s: %w", c.nodeName, err)
	}

	// Sync versions annotations
	if err := c.syncVersionsAnnotations(ctx); err != nil {
		return fmt.Errorf("failed to sync versions annotations: %w", err)
	}

	return nil
}

func (c *Controller) upgradeNode(ctx context.Context) error {
	// Get the node from the lister
	node, err := c.nodesLister.Get(c.nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", c.nodeName, err)
	}

	// Exit if the annotation is not set
	if value, exists := node.Annotations["k8s.scaleway.com/agent"]; !exists || value != "upgrade" {
		return nil
	}

	// The annotation is set, so we need to upgrade the node
	c.logger.Info("Upgrading node")
	c.recorder.Eventf(node, corev1.EventTypeNormal, "NodeUpgrade", "Node upgrading")

	// Get node token to fetch the node metadata
	nodeUserData, err := getNodeUserData()
	if err != nil {
		c.recorder.Eventf(node, corev1.EventTypeWarning, "NodeUpgrade", "Failed to get credentials: %w", err)
		return fmt.Errorf("failed to get credentials: %w", err)
	}

	// Get the node metadata, from the PN node metadata endpoint or the external kapsule endpoint
	nodeMetadata, err := getNodeMetadata(nodeUserData.MetadataURL, nodeUserData.NodeSecretKey)
	if err != nil {
		c.recorder.Eventf(node, corev1.EventTypeWarning, "NodeUpgrade", "Failed to get node metadata: %w", err)
		return fmt.Errorf("failed to get node metadata: %w", err)
	}

	// Install the components: binaries, configuration files, and services
	err = processComponents(ctx, nodeMetadata)
	if err != nil {
		c.recorder.Eventf(node, corev1.EventTypeWarning, "NodeUpgrade", "Failed to install components: %w", err)
		return fmt.Errorf("failed to install components: %w", err)
	}

	// Remove the annotation
	node, err = c.nodesLister.Get(c.nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", c.nodeName, err)
	}
	nodeCopy := node.DeepCopy()
	delete(nodeCopy.Annotations, "k8s.scaleway.com/agent")
	_, err = c.client.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
	if err != nil {
		c.recorder.Eventf(node, corev1.EventTypeWarning, "NodeUpgrade", "Failed to remove annotation: %w", err)
		return fmt.Errorf("failed to remove annotation from node %s: %w", c.nodeName, err)
	}

	c.logger.Info("Node upgraded")
	c.recorder.Event(node, corev1.EventTypeNormal, "NodeUpgrade", "Node upgraded")

	return nil
}

func (c *Controller) syncVersionsAnnotations(ctx context.Context) error {
	// Read installed components versions
	versions, err := ListComponentsVersions()
	if err != nil {
		return fmt.Errorf("failed to list components versions: %w", err)
	}

	// Get the node from the lister
	node, err := c.nodesLister.Get(c.nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", c.nodeName, err)
	}
	nodeCopy := node.DeepCopy()

	// Initialize the annotations map if nil
	if nodeCopy.Annotations == nil {
		nodeCopy.Annotations = make(map[string]string)
	}

	// Set agent version
	versions["agent"] = Version

	// Update the node annotations with the versions
	for component, version := range versions {
		nodeCopy.Annotations[fmt.Sprintf("k8s.scaleway.com/component-%s", component)] = version
	}

	// Remove version annotations for components not installed anymore
	for annotation := range nodeCopy.Annotations {
		// Check if the annotation is for a component version
		if !strings.HasPrefix(annotation, "k8s.scaleway.com/component-") {
			continue
		}

		found := false
		for component := range versions {
			if annotation == fmt.Sprintf("k8s.scaleway.com/component-%s", component) {
				found = true
				break
			}
		}
		if !found {
			delete(nodeCopy.Annotations, annotation)
		}
	}

	// If the annotations are the same, do not update
	if reflect.DeepEqual(node.Annotations, nodeCopy.Annotations) {
		return nil
	}

	// Update the node with the new annotations
	_, err = c.client.CoreV1().Nodes().Update(ctx, nodeCopy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node annotations %s: %w", c.nodeName, err)
	}

	return nil
}
