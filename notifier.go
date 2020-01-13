package ip8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/pkg/errors"
	api "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

func NewInClusterNotifier(resyncDuration time.Duration, selector string) (Notifier, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, errors.Wrap(err, "failed to read cluster config")
	}

	return NewNotifier(config, resyncDuration, selector)
}

func NewNotifier(config *rest.Config, resyncDuration time.Duration, selector string) (Notifier, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return newNotifierFromClient(client, resyncDuration, selector), nil
}

func newNotifierFromClient(client kubernetes.Interface, resyncDuration time.Duration, selector string) Notifier {
	factory := informers.NewSharedInformerFactory(client, resyncDuration)
	nodes := factory.Core().V1().Nodes()
	lister := &nodeLister{nodes.Lister()}
	observer := &nodeObserver{nodes.Informer()}
	return &notifier{observer, lister, selector, false, nil}
}

type Notifier interface {
	Notify(ctx context.Context) <-chan []string
}

type notifier struct {
	observer *nodeObserver
	lister   *nodeLister

	selector   string
	subsequent bool
	lastIPs    []string
}

func diff(one, two []string) bool {
	if len(one) != len(two) {
		return true
	}
	for i := range one {
		if one[i] != two[i] {
			return true
		}
	}
	return false
}

func (n *notifier) sendIPs(c chan<- []string) {
	ips, err := n.lister.List(n.selector)
	if err != nil {
		// TODO: handle error properly
		fmt.Println(err)
		return
	}
	if !n.subsequent || diff(n.lastIPs, ips) {
		n.lastIPs = ips
		c <- ips
	}
	n.subsequent = true
}

func (n *notifier) Notify(ctx context.Context) <-chan []string {
	return n.observer.Observe(ctx, n.sendIPs)
}

type nodeObserver struct {
	informer cache.SharedIndexInformer
}

func (o *nodeObserver) Observe(ctx context.Context, sender func(c chan<- []string)) <-chan []string {
	c := make(chan []string, 128)
	o.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			sender(c)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			sender(c)
		},
		DeleteFunc: func(obj interface{}) {
			sender(c)
		},
	})
	go func() {
		o.informer.Run(ctx.Done())
		close(c)
	}()
	cache.WaitForCacheSync(ctx.Done(), o.informer.HasSynced)
	sender(c)
	return c
}

type nodeLister struct {
	lister v1.NodeLister
}

func (l *nodeLister) List(sel string) ([]string, error) {
	label := labels.NewSelector()
	if sel != "" {
		var err error
		label, err = labels.Parse(sel)
		if err != nil {
			return nil, errors.Wrap(err, "invalid node selector")
		}
	}

	nodes, err := l.lister.List(label)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list the nodes")
	}
	ips := []string{}
	for _, nod := range nodes {
		n := &node{nod}
		if n.Available() {
			ips = append(ips, n.IPs()...)
		}
	}
	sort.Strings(ips)
	return ips, nil
}

type node struct {
	node *api.Node
}

func (n *node) Available() bool {
	for _, condition := range n.node.Status.Conditions {
		if condition.Type == api.NodeReady && condition.Status == api.ConditionTrue {
			return true
		}
	}
	return false
}

func (n *node) IPs() []string {
	var ips []string
	for _, addr := range n.node.Status.Addresses {
		if addr.Type == api.NodeExternalIP {
			ips = append(ips, addr.Address)
		}
	}
	return ips
}
