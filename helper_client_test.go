package ip8s

import (
	"strconv"
	"testing"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakekube "k8s.io/client-go/kubernetes/fake"
)

type nodeBuilder struct {
	conditions []v1.NodeCondition
	addresses  []v1.NodeAddress
}

func buildNode() *nodeBuilder {
	return &nodeBuilder{}
}

func (b *nodeBuilder) Condition(typ v1.NodeConditionType, status v1.ConditionStatus) *nodeBuilder {
	b.conditions = append(b.conditions, v1.NodeCondition{
		Type:   typ,
		Status: status,
	})
	return b
}

func (b *nodeBuilder) Address(typ v1.NodeAddressType, addr string) *nodeBuilder {
	b.addresses = append(b.addresses, v1.NodeAddress{
		Type:    typ,
		Address: addr,
	})
	return b
}

func (b *nodeBuilder) Build(name string) *v1.Node {
	return &v1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: v1.NodeStatus{
			Conditions: b.conditions,
			Addresses:  b.addresses,
		},
	}
}

func helperClient(t *testing.T, args ...[]interface{}) *fakekube.Clientset {
	var nodes []runtime.Object
	for _, arg := range args {
		nodes = append(nodes, helperNewNode(t, arg...))
	}
	client := fakekube.NewSimpleClientset(nodes...)
	return client
}

func helperNewNode(t *testing.T, args ...interface{}) runtime.Object {
	if len(args)%2 != 0 {
		t.Fatal("invalid number of arguments for 'helperNewNode'")
	}
	var conditions []v1.NodeCondition
	var addresses []v1.NodeAddress
	for i := 0; i < len(args); i++ {
		switch first := args[i].(type) {
		case v1.NodeConditionType:
			switch second := args[i+1].(type) {
			case v1.ConditionStatus:
				conditions = append(conditions, v1.NodeCondition{
					Type:   first,
					Status: second,
				})
			default:
				t.Fatal("invalid argument type at index " + strconv.Itoa(i+1))
			}
		case v1.NodeAddressType:
			switch second := args[i+1].(type) {
			case string:
				addresses = append(addresses, v1.NodeAddress{
					Type:    first,
					Address: second,
				})
			default:
				t.Fatal("invalid argument type at index " + strconv.Itoa(i+1))
			}
		default:
			t.Fatal("invalid argument type at index " + strconv.Itoa(i))
		}
	}
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "",
		},
		Status: v1.NodeStatus{
			Conditions: conditions,
			Addresses:  addresses,
		},
	}
}
