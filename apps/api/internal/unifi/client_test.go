package unifi

import "testing"

// TestDeviceTypeClassification locks the model-name heuristic. Review F1:
// Flex-family access points must land on "ap" while Flex-family switches (which
// carry the usw/usl prefix) stay "switch".
func TestDeviceTypeClassification(t *testing.T) {
	cases := []struct {
		name      string
		model     string
		features  []string
		isConsole bool
		want      string
	}{
		// F1 regression: the false positive was "flex" sitting in the switch branch.
		{name: "UAP-FlexHD is an access point", model: "UAP-FlexHD", want: "ap"},
		{name: "FlexHD is an access point", model: "FlexHD", want: "ap"},
		{name: "USW-Flex-Mini is a switch", model: "USW-Flex-Mini", want: "switch"},
		{name: "USW-Flex is a switch", model: "USW-Flex", want: "switch"},
		{name: "USL-Flex is a switch", model: "USL-Flex", want: "switch"},
		{name: "UniFi Switch Flex is a switch", model: "UniFi Switch Flex", want: "switch"},

		// The model families actually present in the estate.
		{name: "AC Pro", model: "AC Pro", want: "ap"},
		{name: "U6 Pro", model: "U6 Pro", want: "ap"},
		{name: "UDM Pro console", model: "UDM Pro", isConsole: true, want: "gateway"},
		{name: "UXG gateway", model: "UXG-Lite", want: "gateway"},
		{name: "USW-24", model: "USW-24", want: "switch"},
		{name: "unknown model", model: "Some Appliance", want: "other"},

		// The Integration API states the type explicitly; that wins.
		{name: "feature accessPoint", model: "Unknown", features: []string{"accessPoint"}, want: "ap"},
		{name: "feature switching", model: "Unknown", features: []string{"switching"}, want: "switch"},
		{name: "feature gateway", model: "Unknown", features: []string{"gateway"}, want: "gateway"},

		// A UDM Pro advertises `switching` yet is a gateway: console flag and
		// gateway model families are decided before the feature list.
		{name: "UDM Pro advertising switching", model: "UDM Pro", features: []string{"switching"}, isConsole: true, want: "gateway"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deviceType(tc.features, tc.model, tc.isConsole)
			if got != tc.want {
				t.Fatalf("deviceType(features=%v, model=%q, isConsole=%v) = %q, want %q",
					tc.features, tc.model, tc.isConsole, got, tc.want)
			}
		})
	}
}
