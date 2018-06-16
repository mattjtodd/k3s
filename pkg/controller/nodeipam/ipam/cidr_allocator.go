/*
Copyright 2016 The Kubernetes Authors.

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

package ipam

import (
	"fmt"
	"net"
	"time"

	"github.com/golang/glog"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	informers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/cloudprovider"
)

type nodeAndCIDR struct {
	cidr     *net.IPNet
	nodeName string
}

// CIDRAllocatorType is the type of the allocator to use.
type CIDRAllocatorType string

const (
	// RangeAllocatorType is the allocator that uses an internal CIDR
	// range allocator to do node CIDR range allocations.
	RangeAllocatorType CIDRAllocatorType = "RangeAllocator"
)

// TODO: figure out the good setting for those constants.
const (
	// The amount of time the nodecontroller polls on the list nodes endpoint.
	apiserverStartupGracePeriod = 10 * time.Minute

	// The no. of NodeSpec updates NC can process concurrently.
	cidrUpdateWorkers = 30

	// The max no. of NodeSpec updates that can be enqueued.
	cidrUpdateQueueSize = 5000

	// cidrUpdateRetries is the no. of times a NodeSpec update will be retried before dropping it.
	cidrUpdateRetries = 3

	// updateRetryTimeout is the time to wait before requeing a failed node for retry
	updateRetryTimeout = 100 * time.Millisecond

	// updateMaxRetries is the max retries for a failed node
	updateMaxRetries = 10
)

// CIDRAllocator is an interface implemented by things that know how
// to allocate/occupy/recycle CIDR for nodes.
type CIDRAllocator interface {
	// AllocateOrOccupyCIDR looks at the given node, assigns it a valid
	// CIDR if it doesn't currently have one or mark the CIDR as used if
	// the node already have one.
	AllocateOrOccupyCIDR(node *v1.Node) error
	// ReleaseCIDR releases the CIDR of the removed node
	ReleaseCIDR(node *v1.Node) error
	// Run starts all the working logic of the allocator.
	Run(stopCh <-chan struct{})
}

// New creates a new CIDR range allocator.
func New(kubeClient clientset.Interface, cloud cloudprovider.Interface, nodeInformer informers.NodeInformer, allocatorType CIDRAllocatorType, clusterCIDR, serviceCIDR *net.IPNet, nodeCIDRMaskSize int) (CIDRAllocator, error) {
	nodeList, err := listNodes(kubeClient)
	if err != nil {
		return nil, err
	}

	switch allocatorType {
	case RangeAllocatorType:
		return NewCIDRRangeAllocator(kubeClient, nodeInformer, clusterCIDR, serviceCIDR, nodeCIDRMaskSize, nodeList)
	default:
		return nil, fmt.Errorf("Invalid CIDR allocator type: %v", allocatorType)
	}
}

func listNodes(kubeClient clientset.Interface) (*v1.NodeList, error) {
	var nodeList *v1.NodeList
	// We must poll because apiserver might not be up. This error causes
	// controller manager to restart.
	if pollErr := wait.Poll(10*time.Second, apiserverStartupGracePeriod, func() (bool, error) {
		var err error
		nodeList, err = kubeClient.CoreV1().Nodes().List(metav1.ListOptions{
			FieldSelector: fields.Everything().String(),
			LabelSelector: labels.Everything().String(),
		})
		if err != nil {
			glog.Errorf("Failed to list all nodes: %v", err)
			return false, nil
		}
		return true, nil
	}); pollErr != nil {
		return nil, fmt.Errorf("Failed to list all nodes in %v, cannot proceed without updating CIDR map",
			apiserverStartupGracePeriod)
	}
	return nodeList, nil
}
