package wsbridge

import "testing"

func TestRunURL(t *testing.T) {
	for _, tc := range []struct{ endpoint, instance, mission, want string }{
		{"wss://cc.example/ws", "instance-1", "run-1", "https://cc.example/instances/instance-1/runs/run-1"},
		{"ws://localhost:8080/ws", "1", "run-1", "http://localhost:8080/instances/1/runs/run-1"},
		{"wss://user:password@cc.example/prefix/ws?token=secret#ignored", "one/two", "run ?#", "https://cc.example/prefix/instances/one%2Ftwo/runs/run%20%3F%23"},
		{"wss://cc.example/ws", "", "run-1", ""},
		{"", "", "run-1", ""},
		{"wss://cc.example/ws", "1", "", ""},
	} {
		c := &Client{commandCenterURL: tc.endpoint, instanceID: tc.instance}
		if got := c.RunURL(tc.mission); got != tc.want {
			t.Errorf("RunURL(%q, %q, %q) = %q, want %q", tc.endpoint, tc.instance, tc.mission, got, tc.want)
		}
	}
}
