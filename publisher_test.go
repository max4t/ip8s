package ip8s

import (
	"testing"
)

type defaultSlackTemplateTestCase struct {
	dns    string
	ips    []string
	result string
}

func TestDefaultSlackTemplate(t *testing.T) {
	testCases := map[string]defaultSlackTemplateTestCase{
		"EmptyIPs": {
			dns:    "example.com",
			ips:    nil,
			result: "IPs for example.com were deleted",
		},
		"OneIP": {
			dns:    "www.example.com",
			ips:    []string{"1.2.3.4"},
			result: "IPs for www.example.com changed:\n_1.2.3.4_",
		},
		"multipleIPs": {
			dns:    "app.example.com",
			ips:    []string{"1.2.3.5", "5.2.3.5", "1.5.3.5"},
			result: "IPs for app.example.com changed:\n_1.2.3.5\t5.2.3.5\t1.5.3.5_",
		},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			res, err := execDefaultSlackTemplate(testCase.dns, testCase.ips)
			if err != nil {
				t.Errorf("template errored: expected <nil> but got %v", err)
			} else if res != testCase.result {
				t.Errorf("template invalid: expected '%s' but got '%s'", testCase.result, res)
			}
		})
	}
}
