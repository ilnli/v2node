package node

import "testing"

func TestCloseBeforeControllersStart(t *testing.T) {
	n := &Node{controllers: []*Controller{{}, {}}}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
}
