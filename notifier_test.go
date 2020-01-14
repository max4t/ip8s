package ip8s

import (
	//fakerest "k8s.io/client-go/rest/fake"
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	fakekube "k8s.io/client-go/kubernetes/fake"
	restclient "k8s.io/client-go/rest"
	testingkube "k8s.io/client-go/testing"
)

type testObject struct {
	runtime.TypeMeta
}

func (o testObject) GetObjectKind() schema.ObjectKind {
	return &o
}

func (o testObject) DeepCopyObject() runtime.Object {
	return o
}

func (o *testObject) SetGroupVersionKind(kind schema.GroupVersionKind) {
	o.APIVersion, o.Kind = kind.ToAPIVersionAndKind()
}

func (o testObject) GroupVersionKind() schema.GroupVersionKind {
	return schema.FromAPIVersionAndKind(o.APIVersion, o.Kind)
}

var healthyNode1 = buildNode().
	Condition(v1.NodeReady, v1.ConditionTrue).
	Address(v1.NodeExternalIP, "1.2.3.4")
var healthyNode2 = buildNode().
	Condition(v1.NodeReady, v1.ConditionTrue).
	Address(v1.NodeExternalIP, "1.2.3.5")
var healthyMultiConditionsNode = buildNode().
	Condition(v1.NodeReady, v1.ConditionTrue).
	Condition(v1.NodeMemoryPressure, v1.ConditionTrue).
	Address(v1.NodeExternalIP, "1.2.3.6")
var healthyMultiAddressesNode = buildNode().
	Condition(v1.NodeReady, v1.ConditionTrue).
	Address(v1.NodeExternalIP, "1.2.3.7").
	Address(v1.NodeExternalIP, "1.2.3.8")
var unhealthyMultiConditionsNode = buildNode().
	Condition(v1.NodeReady, v1.ConditionFalse).
	Condition(v1.NodeMemoryPressure, v1.ConditionTrue).
	Address(v1.NodeExternalIP, "1.2.3.9")
var healthyInternalAddressesNode = buildNode().
	Condition(v1.NodeReady, v1.ConditionTrue).
	Address(v1.NodeExternalIP, "1.2.3.10").
	Address(v1.NodeInternalIP, "1.2.3.11")

type changeType int

const (
	addChange changeType = iota
	modifyChange
	deleteChange
)

type nodeChange struct {
	typ  changeType
	name string
	node *nodeBuilder
}

type notifierTestCase struct {
	initial map[string]*nodeBuilder
	changes []nodeChange
	result  [][]string
}

func helperExtractChan(c <-chan []string) ([]string, bool) {
	select {
	case res := <-c:
		return res, false
	default:
		return nil, true
	}
}

func helperEqual(one, two []string) bool {
	if len(one) != len(two) {
		return false
	}
	for i := range one {
		if one[i] != two[i] {
			return false
		}
	}
	return true
}

func assertChanOfStringList(t *testing.T, expected [][]string, c <-chan []string) {
	i := 0
	for actual := range c {
		if i >= len(expected) {
			t.Errorf("extra step %v", i)
			continue
		}
		assertEqualStep(t, i, expected, actual)
		i++
	}
	if i < len(expected) {
		t.Errorf("not enough changes detected: expected %v but got %v", len(expected), i)
	}
}

func assertEqualStep(t *testing.T, i int, expected [][]string, actual []string) {
	expectedStep := expected[i]
	if !helperEqual(expectedStep, actual) {
		t.Errorf("mismatch at step %v: expected %v but got %v", i, expectedStep, actual)
	}
}

func TestNotifier(t *testing.T) {
	testCases := map[string]notifierTestCase{
		"EmptyNoChange": notifierTestCase{
			result: [][]string{
				{},
			},
		},
		"OneHealthyNoChange": notifierTestCase{
			initial: map[string]*nodeBuilder{
				"node1": healthyNode1,
			},
			result: [][]string{
				{"1.2.3.4"},
			},
		},
		"OneUnhealthyNoChange": notifierTestCase{
			initial: map[string]*nodeBuilder{
				"node1": unhealthyMultiConditionsNode,
			},
			result: [][]string{
				{},
			},
		},
		"EmptyOneHealthy": notifierTestCase{
			changes: []nodeChange{
				{typ: addChange, name: "node1", node: healthyNode1},
			},
			result: [][]string{
				{},
				{"1.2.3.4"},
			},
		},
		"EmptyOneUnhealthy": notifierTestCase{
			changes: []nodeChange{
				{typ: addChange, name: "node1", node: unhealthyMultiConditionsNode},
			},
			result: [][]string{
				{},
			},
		},
		"OneHealthyDeleted": notifierTestCase{
			initial: map[string]*nodeBuilder{
				"node1": healthyNode1,
			},
			changes: []nodeChange{
				{typ: deleteChange, name: "node1", node: healthyNode1},
			},
			result: [][]string{
				{"1.2.3.4"},
				{},
			},
		},
		"OneHealthyUpdatedIP": notifierTestCase{
			initial: map[string]*nodeBuilder{
				"node1": healthyNode1,
			},
			changes: []nodeChange{
				{typ: modifyChange, name: "node1", node: healthyNode2},
			},
			result: [][]string{
				{"1.2.3.4"},
				{"1.2.3.5"},
			},
		},
		"OneHealthyNewHealthy": notifierTestCase{
			initial: map[string]*nodeBuilder{
				"node1": healthyNode1,
			},
			changes: []nodeChange{
				{typ: addChange, name: "node2", node: healthyNode2},
			},
			result: [][]string{
				{"1.2.3.4"},
				{"1.2.3.4", "1.2.3.5"},
			},
		},
		"OneHealthyNewHealthyDelete": notifierTestCase{
			initial: map[string]*nodeBuilder{
				"node1": healthyNode1,
			},
			changes: []nodeChange{
				{typ: addChange, name: "node2", node: healthyNode2},
				{typ: deleteChange, name: "node1", node: healthyNode1},
			},
			result: [][]string{
				{"1.2.3.4"},
				{"1.2.3.4", "1.2.3.5"},
				{"1.2.3.5"},
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			var initState []runtime.Object
			for n, node := range testCase.initial {
				initState = append(initState, node.Build(n))
			}
			client := fakekube.NewSimpleClientset(initState...)
			notifier := newNotifierFromClient(client, time.Second, "")
			ctx, cancel := context.WithCancel(context.Background())
			ipsChan := notifier.Notify(ctx)
			go func(changes []nodeChange) {
				defer cancel()
				tracker := client.Tracker()
				for _, change := range changes {
					curNode := change.node.Build(change.name)
					resource, _ := meta.UnsafeGuessKindToResource(curNode.GetObjectKind().GroupVersionKind())
					switch change.typ {
					case addChange:
						tracker.Create(resource, curNode, "")
					case modifyChange:
						tracker.Update(resource, curNode, "")
					case deleteChange:
						tracker.Delete(resource, "", change.name)
					default:
						t.Fatal("unknown change type")
					}
					<-time.After(50 * time.Microsecond)
				}
			}(testCase.changes)
			assertChanOfStringList(t, testCase.result, ipsChan)
		})
	}
}

func testNotifier2(t *testing.T) {
	client := &fakekube.Clientset{}
	client.AddReactor("list", "nodes", func(action testingkube.Action) (handled bool, ret runtime.Object, err error) {
		o := &v1.NodeList{
			Items: []v1.Node{
				{
					Status: v1.NodeStatus{
						Conditions: []v1.NodeCondition{
							{
								Type:   v1.NodeReady,
								Status: v1.ConditionTrue,
							},
						},
						Addresses: []v1.NodeAddress{
							{
								Type:    v1.NodeExternalIP,
								Address: "1.2.3.4",
							},
						},
					},
				},
			},
		}
		return true, o, nil
	})
	client.AddReactor("*", "*", func(action testingkube.Action) (handled bool, ret runtime.Object, err error) {
		panic(action)
	})
	client.AddWatchReactor("watch", func(action testingkube.Action) (handled bool, ret watch.Interface, err error) {
		panic(action)
	})
	client.AddWatchReactor("*", func(action testingkube.Action) (handled bool, ret watch.Interface, err error) {
		panic(action)
	})
	client.AddProxyReactor("*", func(action testingkube.Action) (handled bool, ret restclient.ResponseWrapper, err error) {
		panic(action)
	})
	notifier := newNotifierFromClient(client, time.Second, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer func() {
		cancel()
		if err := ctx.Err(); err != nil {
			t.Error(err)
		}
	}()
	ipsChan := notifier.Notify(ctx)
	select {
	case ips := <-ipsChan:
		t.Error(ips)
	case <-ctx.Done():
	}
}
